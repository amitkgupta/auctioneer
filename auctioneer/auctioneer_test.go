package auctioneer_test

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/cloudfoundry-incubator/auction/auctionrunner/fake_auctionrunner"
	"github.com/cloudfoundry-incubator/auction/auctiontypes"
	. "github.com/cloudfoundry-incubator/auctioneer/auctioneer"
	"github.com/cloudfoundry-incubator/runtime-schema/bbs/fake_bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/pivotal-golang/lager/lagertest"
	"github.com/tedsuo/ifrit"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
)

const MAX_AUCTION_ROUNDS_FOR_TEST = 10

var _ = Describe("Auctioneer", func() {
	var (
		bbs            *fake_bbs.FakeAuctioneerBBS
		auctioneer     *Auctioneer
		runner         *fake_auctionrunner.FakeAuctionRunner
		process        ifrit.Process
		firstExecutor  models.ExecutorPresence
		secondExecutor models.ExecutorPresence
		thirdExecutor  models.ExecutorPresence
		logger         *lagertest.TestLogger
		startAuction   models.LRPStartAuction
		stopAuction    models.LRPStopAuction
	)

	BeforeEach(func() {
		logger = lagertest.NewTestLogger("test")
		bbs = fake_bbs.NewFakeAuctioneerBBS()

		firstExecutor = models.ExecutorPresence{
			ExecutorID: "first-rep",
			Stack:      "lucid64",
		}

		secondExecutor = models.ExecutorPresence{
			ExecutorID: "second-rep",
			Stack:      ".Net",
		}

		thirdExecutor = models.ExecutorPresence{
			ExecutorID: "third-rep",
			Stack:      "lucid64",
		}

		bbs.Lock()
		bbs.Executors = []models.ExecutorPresence{
			firstExecutor,
			secondExecutor,
			thirdExecutor,
		}
		bbs.Unlock()

		startAuction = models.LRPStartAuction{
			DesiredLRP: models.DesiredLRP{
				ProcessGuid: "my-guid",
				Stack:       "lucid64",
			},
		}

		stopAuction = models.LRPStopAuction{
			ProcessGuid: "my-stop-guid",
		}
	})

	Describe("the lock lifecycle", func() {
		var signals chan os.Signal
		var ready chan struct{}
		var errors chan error

		BeforeEach(func() {
			runner = &fake_auctionrunner.FakeAuctionRunner{}
			auctioneer = New(bbs, runner, 2, MAX_AUCTION_ROUNDS_FOR_TEST, time.Second, logger)
			signals = make(chan os.Signal)
			ready = make(chan struct{})
			errors = make(chan error)

			go func() {
				errors <- auctioneer.Run(signals, ready)
			}()
		})

		AfterEach(func() {
			signals <- syscall.SIGTERM
			close(<-bbs.ReleaseLockChannel)
			Eventually(errors).Should(Receive())
		})

		Context("before the lock is obtained", func() {
			It("should not be ready", func() {
				Consistently(ready).ShouldNot(BeClosed())
			})
		})

		Context("once the lock is obtained", func() {
			BeforeEach(func() {
				bbs.LockChannel <- true
			})

			It("should start watching for start auctions", func() {
				bbs.LRPStartAuctionChan <- startAuction
				Eventually(runner.RunLRPStartAuctionCallCount).ShouldNot(BeZero())
			})

			It("should start watching for stop auctions", func() {
				bbs.LRPStopAuctionChan <- stopAuction
				Eventually(runner.RunLRPStopAuctionCallCount).ShouldNot(BeZero())
			})

			It("should become ready", func() {
				Eventually(ready).Should(BeClosed())
			})

			Context("if the start watch channel is closed", func() {
				BeforeEach(func() {
					close(bbs.LRPStartAuctionChan)
					time.Sleep(10 * time.Millisecond) //make sure this gets processed
				})

				It("should start watching again on the next lock tick", func() {
					bbs.Lock()
					bbs.LRPStartAuctionChan = make(chan models.LRPStartAuction)
					bbs.Unlock()
					bbs.LockChannel <- true
					bbs.LRPStartAuctionChan <- startAuction
					Eventually(runner.RunLRPStartAuctionCallCount).ShouldNot(BeZero())
				})
			})

			Context("if the stop watch channel is closed", func() {
				BeforeEach(func() {
					close(bbs.LRPStopAuctionChan)
					time.Sleep(10 * time.Millisecond) //make sure this gets processed
				})

				It("should start watching again on the next lock tick", func() {
					bbs.Lock()
					bbs.LRPStopAuctionChan = make(chan models.LRPStopAuction)
					bbs.Unlock()
					bbs.LockChannel <- true
					bbs.LRPStopAuctionChan <- stopAuction
					Eventually(runner.RunLRPStopAuctionCallCount).ShouldNot(BeZero())
				})
			})

			Context("if the start auction watch errors", func() {
				BeforeEach(func() {
					bbs.LRPStartAuctionErrorChan <- fmt.Errorf("boom")
				})

				It("should start watching again on the next lock tick", func() {
					bbs.LRPStartAuctionChan = make(chan models.LRPStartAuction)
					bbs.LockChannel <- true
					bbs.LRPStartAuctionChan <- startAuction
					Eventually(runner.RunLRPStartAuctionCallCount).ShouldNot(BeZero())
				})
			})

			Context("if the stop auction watch errors", func() {
				BeforeEach(func() {
					bbs.LRPStopAuctionErrorChan <- fmt.Errorf("boom")
				})

				It("should start watching again on the next lock tick", func() {
					bbs.LRPStopAuctionChan = make(chan models.LRPStopAuction)
					bbs.LockChannel <- true
					bbs.LRPStopAuctionChan <- stopAuction
					Eventually(runner.RunLRPStopAuctionCallCount).ShouldNot(BeZero())
				})
			})

			Context("if the lock is lost", func() {
				BeforeEach(func() {
					bbs.LockChannel <- false
				})

				It("should stop watching", func() {
					Eventually(bbs.LRPStartAuctionStopChan).Should(BeClosed())
				})

				Context("when the lock is regained", func() {
					BeforeEach(func() {
						bbs.LRPStartAuctionChan = make(chan models.LRPStartAuction)
						bbs.LRPStartAuctionStopChan = make(chan bool)

						bbs.LRPStopAuctionChan = make(chan models.LRPStopAuction)
						bbs.LRPStopAuctionStopChan = make(chan bool)

						bbs.LockChannel <- true
					})

					It("should start watching again", func() {
						bbs.LRPStartAuctionChan <- startAuction
						Eventually(runner.RunLRPStartAuctionCallCount).ShouldNot(BeZero())
						bbs.LRPStopAuctionChan <- stopAuction
						Eventually(runner.RunLRPStopAuctionCallCount).ShouldNot(BeZero())
					})
				})
			})
		})
	})

	Describe("the start auction lifecycle", func() {
		BeforeEach(func() {
			runner = &fake_auctionrunner.FakeAuctionRunner{}
			auctioneer = New(bbs, runner, 2, MAX_AUCTION_ROUNDS_FOR_TEST, time.Second, logger)

			go func() {
				bbs.LockChannel <- true
			}()

			process = ifrit.Envoke(auctioneer)
		})

		AfterEach(func(done Done) {
			//send a shut down signal
			process.Signal(syscall.SIGTERM)
			//which releases the lock, which we need to acknowledge by closing the channel sent to the release lock channel
			close(<-bbs.ReleaseLockChannel)
			//which (eventually) causes the process to exit
			Eventually(process.Wait()).Should(Receive())
			//and should stop the auction
			Ω(bbs.LRPStartAuctionStopChan).Should(BeClosed())

			close(done)
		})

		Context("when a pending auction request arrives over ETCD", func() {
			JustBeforeEach(func(done Done) {
				bbs.LRPStartAuctionChan <- startAuction
				close(done)
			})

			It("should attempt to claim the auction", func() {
				Eventually(bbs.GetClaimedLRPStartAuctions).Should(Equal([]models.LRPStartAuction{startAuction}))
			})

			Context("when the claim succeeds", func() {
				It("should run the auction with reps of the proper stack", func() {
					Eventually(runner.RunLRPStartAuctionCallCount).ShouldNot(BeZero())

					request := runner.RunLRPStartAuctionArgsForCall(0)
					Ω(request.LRPStartAuction).Should(Equal(startAuction))
					Ω(request.RepGuids).Should(HaveLen(2))
					Ω(request.RepGuids).Should(ContainElement(firstExecutor.ExecutorID))
					Ω(request.RepGuids).Should(ContainElement(thirdExecutor.ExecutorID))
					Ω(request.RepGuids).ShouldNot(ContainElement(secondExecutor.ExecutorID))
					Ω(request.Rules.Algorithm).Should(Equal("reserve_n_best"))
					Ω(request.Rules.MaxBiddingPoolFraction).Should(Equal(0.2))
					Ω(request.Rules.MaxRounds).Should(Equal(MAX_AUCTION_ROUNDS_FOR_TEST))
				})

				Context("when the auction succeeds", func() {
					It("should resolve the auction in etcd", func() {
						Eventually(bbs.GetResolvedLRPStartAuction).Should(Equal(startAuction))
					})
				})

				Context("when the auction fails", func() {
					BeforeEach(func() {
						runner.RunLRPStartAuctionReturns(auctiontypes.StartAuctionResult{}, errors.New("the auction failed"))
					})

					It("should log that the auction failed and nontheless resolve the auction", func() {
						Eventually(bbs.GetResolvedLRPStartAuction).Should(Equal(startAuction))

						Ω(logger.TestSink.Buffer).Should(gbytes.Say("auction-failed"))
					})
				})
			})

			Context("when the claim fails", func() {
				BeforeEach(func() {
					bbs.Lock()
					bbs.ClaimLRPStartAuctionError = errors.New("already claimed")
					bbs.Unlock()
				})

				It("should not run the auction", func() {
					Consistently(runner.RunLRPStartAuctionCallCount).Should(BeZero())
				})
			})
		})

		Describe("Sad cases", func() {
			Context("when there are no reps that match the desired stack", func() {
				BeforeEach(func(done Done) {
					startAuction = models.LRPStartAuction{
						DesiredLRP: models.DesiredLRP{
							ProcessGuid: "my-guid",
							Stack:       "monkey-bunnies",
						},
					}
					bbs.LRPStartAuctionChan <- startAuction

					Eventually(bbs.GetClaimedLRPStartAuctions).Should(Equal([]models.LRPStartAuction{startAuction}))
					close(done)
				})

				It("should not run the auction", func() {
					Consistently(runner.RunLRPStartAuctionCallCount).Should(BeZero())
				})

				It("should nonetheless resolve the auction in etcd", func() {
					Eventually(bbs.GetResolvedLRPStartAuction).Should(Equal(startAuction))
				})
			})
		})
	})

	Describe("rate limiting many auctions", func() {
		var startAuction1, startAuction2, startAuction3 models.LRPStartAuction

		BeforeEach(func() {
			runner = &fake_auctionrunner.FakeAuctionRunner{}
			runner.RunLRPStartAuctionStub = func(auctionRequest auctiontypes.StartAuctionRequest) (auctiontypes.StartAuctionResult, error) {
				time.Sleep(time.Second)
				return auctiontypes.StartAuctionResult{}, nil
			}

			auctioneer = New(bbs, runner, 2, MAX_AUCTION_ROUNDS_FOR_TEST, time.Second, logger)

			go func() {
				bbs.LockChannel <- true
			}()

			process = ifrit.Envoke(auctioneer)

			startAuction1 = models.LRPStartAuction{
				DesiredLRP: models.DesiredLRP{
					ProcessGuid: "my-guid-1",
					Stack:       "lucid64",
				},
			}
			startAuction2 = models.LRPStartAuction{
				DesiredLRP: models.DesiredLRP{
					ProcessGuid: "my-guid-2",
					Stack:       "lucid64",
				},
			}
			startAuction3 = models.LRPStartAuction{
				DesiredLRP: models.DesiredLRP{
					ProcessGuid: "my-guid-3",
					Stack:       "lucid64",
				},
			}
		})

		AfterEach(func() {
			process.Signal(syscall.SIGTERM)
			close(<-bbs.ReleaseLockChannel)
			<-process.Wait()
		})

		It("should only process maxConcurrent auctions at a time", func() {
			bbs.LRPStartAuctionChan <- startAuction1
			bbs.LRPStartAuctionChan <- startAuction2
			bbs.LRPStartAuctionChan <- startAuction3

			Eventually(bbs.GetClaimedLRPStartAuctions).Should(HaveLen(2))
			Consistently(bbs.GetClaimedLRPStartAuctions, 0.5).Should(HaveLen(2))

			Eventually(bbs.GetClaimedLRPStartAuctions).Should(HaveLen(3))
		})
	})

	Describe("the stop auction lifecycle", func() {
		BeforeEach(func() {
			runner = &fake_auctionrunner.FakeAuctionRunner{}
			auctioneer = New(bbs, runner, 2, MAX_AUCTION_ROUNDS_FOR_TEST, time.Second, logger)

			go func() {
				bbs.LockChannel <- true
			}()

			process = ifrit.Envoke(auctioneer)
		})

		AfterEach(func(done Done) {
			//send a shut down signal
			process.Signal(syscall.SIGTERM)
			//which releases the lock, which we need to acknowledge by closing the channel sent to the release lock channel
			close(<-bbs.ReleaseLockChannel)
			//which (eventually) causes the process to exit
			Eventually(process.Wait()).Should(Receive())
			//and should stop the auction
			Ω(bbs.LRPStopAuctionStopChan).Should(BeClosed())

			close(done)
		})

		Context("when a pending auction request arrives over ETCD", func() {
			JustBeforeEach(func() {
				bbs.LRPStopAuctionChan <- stopAuction
			})

			It("should attempt to claim the auction", func() {
				Eventually(bbs.GetClaimedLRPStopAuctions).Should(Equal([]models.LRPStopAuction{stopAuction}))
			})

			Context("when the claim succeeds", func() {
				It("should run the auction with reps of the proper stack", func() {
					Eventually(runner.RunLRPStopAuctionCallCount).ShouldNot(BeZero())

					request := runner.RunLRPStopAuctionArgsForCall(0)
					Ω(request.LRPStopAuction).Should(Equal(stopAuction))
					Ω(request.RepGuids).Should(HaveLen(3))
					Ω(request.RepGuids).Should(ContainElement(firstExecutor.ExecutorID))
					Ω(request.RepGuids).Should(ContainElement(secondExecutor.ExecutorID))
					Ω(request.RepGuids).Should(ContainElement(thirdExecutor.ExecutorID))
				})

				Context("when the auction succeeds", func() {
					It("should resolve the auction in etcd", func() {
						Eventually(bbs.GetResolvedLRPStopAuction).Should(Equal(stopAuction))
					})
				})

				Context("when the auction fails", func() {
					BeforeEach(func() {
						runner.RunLRPStopAuctionReturns(auctiontypes.StopAuctionResult{}, errors.New("the auction failed"))
					})

					It("should log that the auction failed and nontheless resolve the auction", func() {
						Eventually(bbs.GetResolvedLRPStopAuction).Should(Equal(stopAuction))

						Ω(logger.TestSink.Buffer).Should(gbytes.Say("auction-failed"))
					})
				})
			})

			Context("when the claim fails", func() {
				BeforeEach(func() {
					bbs.Lock()
					bbs.ClaimLRPStopAuctionError = errors.New("already claimed")
					bbs.Unlock()
				})

				It("should not run the auction", func() {
					Consistently(runner.RunLRPStopAuctionCallCount).Should(BeZero())
				})
			})
		})
	})
})
