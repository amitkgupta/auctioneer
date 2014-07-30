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
				DesiredLRP: models.DesiredLRP{
					ProcessGuid: "app-guid",
					DiskMB:      1,
					MemoryMB:    1,
					Stack:       lucidStack,
					Actions:     dummyActions,
					Instances:   2,
				},

				InstanceGuid: "instance-guid-1",
				Index:        0,
				NumAZs:       4,
			})

			bbs.RequestLRPStartAuction(models.LRPStartAuction{
				DesiredLRP: models.DesiredLRP{
					ProcessGuid: "app-guid",
					DiskMB:      1,
					MemoryMB:    1,
					Stack:       lucidStack,
					Actions:     dummyActions,
					Instances:   2,
				},

				InstanceGuid: "instance-guid-2",
				Index:        1,
				NumAZs:       4,
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
				DesiredLRP: models.DesiredLRP{
					ProcessGuid: "app-guid",
					DiskMB:      1,
					MemoryMB:    1,
					Stack:       lucidStack,
					Actions:     dummyActions,
					Instances:   1,
				},

				InstanceGuid: "duplicate-instance-guid-1",
				Index:        0,
				NumAZs:       4,
			})

			Eventually(bbs.GetAllLRPStartAuctions).Should(HaveLen(0))

			bbs.RequestLRPStartAuction(models.LRPStartAuction{
				DesiredLRP: models.DesiredLRP{
					ProcessGuid: "app-guid",
					DiskMB:      1,
					MemoryMB:    1,
					Stack:       lucidStack,
					Actions:     dummyActions,
					Instances:   1,
				},

				InstanceGuid: "duplicate-instance-guid-2",
				Index:        0,
				NumAZs:       4,
			})

			Eventually(bbs.GetAllLRPStartAuctions).Should(HaveLen(0))

			bbs.RequestLRPStartAuction(models.LRPStartAuction{
				DesiredLRP: models.DesiredLRP{
					ProcessGuid: "app-guid",
					DiskMB:      1,
					MemoryMB:    1,
					Stack:       lucidStack,
					Actions:     dummyActions,
					Instances:   1,
				},

				InstanceGuid: "duplicate-instance-guid-3",
				Index:        0,
				NumAZs:       4,
			})

			Eventually(bbs.GetAllLRPStartAuctions).Should(HaveLen(0))

			Ω(repClient.SimulatedInstances(lucidGuid)).Should(HaveLen(3))
		})

		It("should stop all but one instance of the app", func() {
			bbs.RequestLRPStopAuction(models.LRPStopAuction{
				ProcessGuid:  "app-guid",
				Index:        0,
				NumInstances: 0,
				NumAZs:       4,
			})

			Eventually(func() interface{} {
				return repClient.SimulatedInstances(lucidGuid)
			}, 1).Should(HaveLen(1))
		})
	})

})
