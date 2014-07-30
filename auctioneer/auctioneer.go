package auctioneer

import (
	"os"
	"syscall"
	"time"

	"github.com/cloudfoundry-incubator/auction/auctionrunner"
	"github.com/cloudfoundry-incubator/auction/auctiontypes"
	"github.com/nu7hatch/gouuid"
	"github.com/pivotal-golang/lager"

	Bbs "github.com/cloudfoundry-incubator/runtime-schema/bbs"

	"github.com/cloudfoundry-incubator/runtime-schema/models"
)

type Auctioneer struct {
	bbs           Bbs.AuctioneerBBS
	runner        auctiontypes.AuctionRunner
	maxConcurrent int
	maxRounds     int
	logger        lager.Logger
	semaphore     chan bool
	lockInterval  time.Duration
}

func New(bbs Bbs.AuctioneerBBS, runner auctiontypes.AuctionRunner, maxConcurrent int, maxRounds int, lockInterval time.Duration, logger lager.Logger) *Auctioneer {
	return &Auctioneer{
		bbs:           bbs,
		runner:        runner,
		maxConcurrent: maxConcurrent,
		maxRounds:     maxRounds,
		logger:        logger.Session("auctioneer"),
		semaphore:     make(chan bool, maxConcurrent),
		lockInterval:  lockInterval,
	}
}

func (a *Auctioneer) Run(signals <-chan os.Signal, ready chan<- struct{}) error {
	guid, err := uuid.NewV4()
	if err != nil {
		return err
	}

	haveLockChan, stopMaintainingLockChan, err := a.bbs.MaintainAuctioneerLock(a.lockInterval, guid.String())
	if err != nil {
		return err
	}

	var startAuctionChan <-chan models.LRPStartAuction
	var startErrorChan <-chan error
	var cancelStartWatchChan chan<- bool

	var stopAuctionChan <-chan models.LRPStopAuction
	var stopErrorChan <-chan error
	var cancelStopWatchChan chan<- bool

	for {
		select {
		case haveLock := <-haveLockChan:
			a.logger.Info("lock-state", lager.Data{"have-lock": haveLock})

			if haveLock {
				if startAuctionChan == nil {
					startAuctionChan, cancelStartWatchChan, startErrorChan = a.bbs.WatchForLRPStartAuction()

					a.logger.Info("watching-for-start-auctions")
				}

				if stopAuctionChan == nil {
					stopAuctionChan, cancelStopWatchChan, stopErrorChan = a.bbs.WatchForLRPStopAuction()

					a.logger.Info("watching-for-stop-auctions")
				}

				if ready != nil {
					close(ready)
					ready = nil
				}
			} else {
				if startAuctionChan != nil {
					close(cancelStartWatchChan)
					startAuctionChan, cancelStartWatchChan, startErrorChan = nil, nil, nil
				}

				if stopAuctionChan != nil {
					close(cancelStopWatchChan)
					stopAuctionChan, cancelStopWatchChan, stopErrorChan = nil, nil, nil
				}
			}

		case startAuction, ok := <-startAuctionChan:
			if !ok {
				startAuctionChan = nil
				continue
			}

			logger := a.logger.Session("start", lager.Data{
				"start-auction": startAuction,
			})

			go a.runStartAuction(startAuction, logger)

		case stopAuction, ok := <-stopAuctionChan:
			if !ok {
				stopAuctionChan = nil
				continue
			}

			logger := a.logger.Session("stop", lager.Data{
				"stop-auction": stopAuction,
			})

			go a.runStopAuction(stopAuction, logger)

		case err := <-startErrorChan:
			a.logger.Error("watching-start-auctions-failed", err)
			startAuctionChan = nil

		case err := <-stopErrorChan:
			a.logger.Error("watching-stop-auctions-failed", err)
			stopAuctionChan = nil

		case sig := <-signals:
			if a.shouldStop(sig) {
				a.logger.Info("releasing-lock")
				stoppedMaintainingLockChan := make(chan bool)
				stopMaintainingLockChan <- stoppedMaintainingLockChan
				<-stoppedMaintainingLockChan
				if cancelStartWatchChan != nil {
					a.logger.Info("stopping-start-watch")
					close(cancelStartWatchChan)
				}
				if cancelStopWatchChan != nil {
					a.logger.Info("stopping-stop-watch")
					close(cancelStopWatchChan)
				}
				return nil
			}
		}
	}

	return nil
}

func (a *Auctioneer) shouldStop(sig os.Signal) bool {
	return sig == syscall.SIGINT || sig == syscall.SIGTERM
}

func (a *Auctioneer) runStartAuction(startAuction models.LRPStartAuction, logger lager.Logger) {
	a.semaphore <- true
	defer func() {
		<-a.semaphore
	}()

	logger.Info("received")

	//claim
	err := a.bbs.ClaimLRPStartAuction(startAuction)
	if err != nil {
		logger.Debug("failed-to-claim", lager.Data{"error": err.Error()})
		return
	}

	defer a.bbs.ResolveLRPStartAuction(startAuction)

	executorGuids, err := a.getExecutorsforStack(startAuction.DesiredLRP.Stack)
	if err != nil {
		logger.Error("failed-to-get-executors", err)
		return
	}
	if len(executorGuids) == 0 {
		logger.Error("no-available-executors", nil)
		return
	}

	//perform auction
	logger.Info("performing")

	rules := auctionrunner.DefaultStartAuctionRules
	rules.MaxRounds = a.maxRounds

	request := auctiontypes.StartAuctionRequest{
		LRPStartAuction: startAuction,
		RepGuids:        executorGuids,
		Rules:           rules,
	}

	_, err = a.runner.RunLRPStartAuction(request)
	if err != nil {
		logger.Error("auction-failed", err)
		return
	}
}

func (a *Auctioneer) getExecutorsforStack(stack string) ([]string, error) {
	executors, err := a.bbs.GetAllExecutors()
	if err != nil {
		return nil, err
	}

	filteredExecutorGuids := []string{}

	for _, executor := range executors {
		if executor.Stack == stack {
			filteredExecutorGuids = append(filteredExecutorGuids, executor.ExecutorID)
		}
	}

	return filteredExecutorGuids, nil
}

func (a *Auctioneer) runStopAuction(stopAuction models.LRPStopAuction, logger lager.Logger) {
	logger.Debug("received")

	//claim
	err := a.bbs.ClaimLRPStopAuction(stopAuction)
	if err != nil {
		logger.Debug("failed-to-claim", lager.Data{"error": err.Error()})
		return
	}

	defer a.bbs.ResolveLRPStopAuction(stopAuction)

	executorGuids, err := a.getExecutors()
	if err != nil {
		logger.Error("failed-to-get-executors", err)
		return
	}

	if len(executorGuids) == 0 {
		logger.Error("no-available-executors", nil)
		return
	}

	//perform auction
	logger.Info("perform")

	request := auctiontypes.StopAuctionRequest{
		LRPStopAuction: stopAuction,
		RepGuids:       executorGuids,
	}
	_, err = a.runner.RunLRPStopAuction(request)

	if err != nil {
		logger.Error("auction-failed", err)
		return
	}
}

func (a *Auctioneer) getExecutors() ([]string, error) {
	executors, err := a.bbs.GetAllExecutors()
	if err != nil {
		return nil, err
	}

	executorGuids := []string{}

	for _, executor := range executors {
		executorGuids = append(executorGuids, executor.ExecutorID)
	}

	return executorGuids, nil
}
