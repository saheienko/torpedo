package tests

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/portworx/torpedo/drivers/node"
	"github.com/portworx/torpedo/drivers/scheduler"
	. "github.com/portworx/torpedo/tests"
)

func TestStopScheduler(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Torpedo: StopScheduler")
}

var _ = BeforeSuite(func() {
	InitInstance()
})

var _ = Describe("{StopScheduler}", func() {
	testName := "stopscheduler"
	var contexts []*scheduler.Context

	It("has to stop scheduler service and check if applications are fine", func() {
		var err error
		for i := 0; i < Inst().ScaleFactor; i++ {
			contexts = append(contexts, ScheduleApps(fmt.Sprintf("%s-%d", testName, i))...)
		}
		ValidateApps(fmt.Sprintf("validate apps for %s", CurrentGinkgoTestDescription().TestText), contexts)

		Step("get nodes for all apps in test and induce scheduler service to stop on one of the nodes", func() {
			for _, ctx := range contexts {
				var appNodes []node.Node

				Step(fmt.Sprintf("get nodes where %s app is running", ctx.App.Key), func() {
					appNodes, err = Inst().S.GetNodesForApp(ctx)
					Expect(err).NotTo(HaveOccurred())
					Expect(appNodes).NotTo(BeEmpty())
				})
				randNode := rand.Intn(len(appNodes))
				appNode := appNodes[randNode]
				Step(fmt.Sprintf("stop scheduler service"), func() {
					err := Inst().S.StopSchedOnNode(appNode)
					Expect(err).NotTo(HaveOccurred())
					Step("wait for the service to stop and reschedule apps", func() {
						time.Sleep(6 * time.Minute)
					})

					Step(fmt.Sprintf("check if apps are running"), func() {
						ValidateContext(ctx)
					})
				})

				Step(fmt.Sprintf("start scheduler service"), func() {
					err := Inst().S.StartSchedOnNode(appNode)
					Expect(err).NotTo(HaveOccurred())
				})
			}
		})

		ValidateApps(fmt.Sprintf("validate apps for %s", CurrentGinkgoTestDescription().TestText), contexts)
	})
	AfterEach(func() {
		TearDownAfterEachSpec(contexts)
	})

	JustAfterEach(func() {
		DescribeNamespaceJustAfterEachSpec(contexts)
	})
})

var _ = AfterSuite(func() {
	PerformSystemCheck()
	CollectSupport()
	ValidateCleanup()
})

func init() {
	ParseFlags()
}
