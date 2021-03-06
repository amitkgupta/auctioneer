package integration_test

import (
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var dummyActions = []models.ExecutorAction{
	{
		Action: models.RunAction{
			Path: "cat",
			Args: []string{"/tmp/file"},
		},
	},
}

var _ = Describe("Integration", func() {
	Context("when a start auction message arrives", func() {
		BeforeEach(func() {
			bbs.RequestLRPStartAuction(models.LRPStartAuction{
				ProcessGuid:  "app-guid",
				InstanceGuid: "instance-guid-1",
				DiskMB:       1,
				MemoryMB:     1,
				Stack:        lucidStack,
				Index:        0,
				Actions:      dummyActions,
			})

			bbs.RequestLRPStartAuction(models.LRPStartAuction{
				ProcessGuid:  "app-guid",
				InstanceGuid: "instance-guid-2",
				DiskMB:       1,
				MemoryMB:     1,
				Stack:        lucidStack,
				Index:        1,
				Actions:      dummyActions,
			})
		})

		It("should start the app running on reps of the appropriate stack", func() {
			Eventually(func() interface{} {
				return repClient.SimulatedInstances(lucidGuid)
			}, 1).Should(HaveLen(2))

			Ω(repClient.SimulatedInstances(dotNetGuid)).Should(BeEmpty())
		})
	})

	Context("when a stop auction message arrives", func() {
		BeforeEach(func() {
			bbs.RequestLRPStartAuction(models.LRPStartAuction{
				ProcessGuid:  "app-guid",
				InstanceGuid: "duplicate-instance-guid-1",
				DiskMB:       1,
				MemoryMB:     1,
				Stack:        lucidStack,
				Index:        0,
				Actions:      dummyActions,
			})

			Eventually(bbs.GetAllLRPStartAuctions).Should(HaveLen(0))

			bbs.RequestLRPStartAuction(models.LRPStartAuction{
				ProcessGuid:  "app-guid",
				InstanceGuid: "duplicate-instance-guid-2",
				DiskMB:       1,
				MemoryMB:     1,
				Stack:        lucidStack,
				Index:        0,
				Actions:      dummyActions,
			})

			Eventually(bbs.GetAllLRPStartAuctions).Should(HaveLen(0))

			bbs.RequestLRPStartAuction(models.LRPStartAuction{
				ProcessGuid:  "app-guid",
				InstanceGuid: "duplicate-instance-guid-3",
				DiskMB:       1,
				MemoryMB:     1,
				Stack:        lucidStack,
				Index:        0,
				Actions:      dummyActions,
			})

			Eventually(bbs.GetAllLRPStartAuctions).Should(HaveLen(0))

			Ω(repClient.SimulatedInstances(lucidGuid)).Should(HaveLen(3))
		})

		It("should stop all but one instance of the app", func() {
			bbs.RequestLRPStopAuction(models.LRPStopAuction{
				ProcessGuid: "app-guid",
				Index:       0,
			})

			Eventually(func() interface{} {
				return repClient.SimulatedInstances(lucidGuid)
			}, 1).Should(HaveLen(1))
		})
	})

})
