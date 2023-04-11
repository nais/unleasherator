package controllers

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	unleashv1 "github.com/nais/unleasherator/api/v1"
)

var _ = Describe("Unleash controller", func() {

	// Define utility constants for object names and testing timeouts/durations and intervals.
	const (
		UnleashName      = "test-unleash"
		UnleashNamespace = "default"

		timeout  = time.Second * 10
		duration = time.Second * 10
		interval = time.Millisecond * 250
	)

	Context("When updating Unleash Status", func() {
		It("Should increase Unleash Status.Active count when new Jobs are created", func() {
			By("By creating a new Unleash")
			ctx := context.Background()
			unleash := &unleashv1.Unleash{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "unleash.nais.io/v1",
					Kind:       "Unleash",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      UnleashName,
					Namespace: UnleashNamespace,
				},
				Spec: unleashv1.UnleashSpec{
					Size: 1,
				},
			}
			Expect(k8sClient.Create(ctx, unleash)).Should(Succeed())

			unleashLookupKey := types.NamespacedName{Name: UnleashName, Namespace: UnleashNamespace}
			createUnleash := &unleashv1.Unleash{}

			// We'll need to retry getting this newly created Unleash, given that creation may not immediately happen.
			Eventually(func() bool {
				err := k8sClient.Get(ctx, unleashLookupKey, createUnleash)
				return err == nil
			}, timeout, interval).Should(BeTrue())
			// Let's make sure our Schedule string value was properly converted/handled.
			Expect(createUnleash.Spec.Size).Should(Equal(int32(1)))

			Expect(false).Should(BeTrue())

			//By("By checking the CronJob has zero active Jobs")
			//Consistently(func() (int, error) {
			//	err := k8sClient.Get(ctx, cronjobLookupKey, createdCronjob)
			//	if err != nil {
			//		return -1, err
			//	}
			//	return len(createdCronjob.Status.Active), nil
			//}, duration, interval).Should(Equal(0))

			//By("By creating a new Job")
			//testJob := &batchv1.Job{
			//	ObjectMeta: metav1.ObjectMeta{
			//		Name:      JobName,
			//		Namespace: CronjobNamespace,
			//	},
			//	Spec: batchv1.JobSpec{
			//		Template: v1.PodTemplateSpec{
			//			Spec: v1.PodSpec{
			//				// For simplicity, we only fill out the required fields.
			//				Containers: []v1.Container{
			//					{
			//						Name:  "test-container",
			//						Image: "test-image",
			//					},
			//				},
			//				RestartPolicy: v1.RestartPolicyOnFailure,
			//			},
			//		},
			//	},
			//	Status: batchv1.JobStatus{
			//		Active: 2,
			//	},
			//}

			//// Note that your CronJob’s GroupVersionKind is required to set up this owner reference.
			//kind := reflect.TypeOf(cronjobv1.CronJob{}).Name()
			//gvk := cronjobv1.GroupVersion.WithKind(kind)

			//controllerRef := metav1.NewControllerRef(createdCronjob, gvk)
			//testJob.SetOwnerReferences([]metav1.OwnerReference{*controllerRef})
			//Expect(k8sClient.Create(ctx, testJob)).Should(Succeed())

			//By("By checking that the CronJob has one active Job")
			//Eventually(func() ([]string, error) {
			//	err := k8sClient.Get(ctx, cronjobLookupKey, createdCronjob)
			//	if err != nil {
			//		return nil, err
			//	}

			//	names := []string{}
			//	for _, job := range createdCronjob.Status.Active {
			//		names = append(names, job.Name)
			//	}
			//	return names, nil
			//}, timeout, interval).Should(ConsistOf(JobName), "should list our active job %s in the active jobs list in status", JobName)
		})
	})

})
