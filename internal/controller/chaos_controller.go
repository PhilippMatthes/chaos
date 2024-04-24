/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"math/rand"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	chaosv1 "github.com/philippmatthes/chaos/api/v1"
)

// ChaosReconciler reconciles a Chaos object
type ChaosReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=chaos.chaos.philippmatthes,resources=chaos,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=chaos.chaos.philippmatthes,resources=chaos/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=chaos.chaos.philippmatthes,resources=chaos/finalizers,verbs=update

// Needs privileges to watch and modify Pods in the cluster.
//+kubebuilder:rbac:groups=chaos.chaos.philippmatthes,resources=pods,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Chaos object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.2/pkg/reconcile
func (r *ChaosReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Get all necessary objects from the context first.
	// If something fails, we can early return.
	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(req.Namespace)); err != nil {
		log.Error(err, "unable to list pods")
		return ctrl.Result{}, err
	}
	var chaos chaosv1.Chaos // Our custom resource
	if err := r.Get(ctx, req.NamespacedName, &chaos); err != nil {
		log.Error(err, "unable to fetch Chaos")
		return ctrl.Result{}, err
	}

	// If the last time the chaos was evoked is zero,
	// its the first time we see this function.
	if chaos.Status.LastTriggered.IsZero() {
		chaos.Status.LastTriggered = metav1.Now()
		if err := r.Status().Update(ctx, &chaos); err != nil {
			log.Error(err, "unable to update Chaos status")
			return ctrl.Result{}, err
		}
		log.Info("initialized chaos at time", "time", chaos.Status.LastTriggered.Time)
		return ctrl.Result{}, nil
	}

	// Otherwise check if LastTriggered + Interval is in the past.
	lastTriggeredMs := chaos.Status.LastTriggered.Time.UnixMilli()
	intervalMs := int64(chaos.Spec.Interval) * 1000
	if lastTriggeredMs+intervalMs > time.Now().UnixMilli() {
		log.Info("chaos is not due yet")
		return ctrl.Result{}, nil
	}

	log.Info("evoking chaos")

	// Update the last triggered time.
	chaos.Status.LastTriggered = metav1.Now()
	if err := r.Status().Update(ctx, &chaos); err != nil {
		log.Error(err, "unable to update Chaos status")
		return ctrl.Result{}, err
	}

	nextSchedule := ctrl.Result{
		RequeueAfter: time.Duration(intervalMs) * time.Millisecond,
	}

	// Choose a pod to kill.
	podToKill, err := r.ChoosePodToKill(pods)
	if err != nil {
		log.Error(err, "unable to choose pod to kill")
		return nextSchedule, err
	}

	// If we can't find a pod to kill, we can early return.
	if podToKill == nil {
		log.Info("no pod to kill")
		return nextSchedule, nil
	}

	// Log the pod we're going to kill.
	log.Info("evoking chaos on pod", "pod", podToKill.Name)

	// Delete the pod.
	if err := r.Delete(ctx, podToKill); err != nil {
		log.Error(err, "unable to delete pod", "pod", podToKill.Name)
		return ctrl.Result{}, err
	}

	return nextSchedule, nil
}

// Randomly choose a running pod to kill.
func (r *ChaosReconciler) ChoosePodToKill(pods corev1.PodList) (*corev1.Pod, error) {
	runningPods := []corev1.Pod{}
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodRunning {
			runningPods = append(runningPods, pod)
		}
	}
	// If there are no running pods, we can't kill any.
	if len(runningPods) == 0 {
		return nil, nil
	}
	// Generate a random index between 0 and the number of pods.
	podIdx := rand.Intn(len(runningPods))
	return &runningPods[podIdx], nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ChaosReconciler) SetupWithManager(mgr ctrl.Manager) error {
	fe := mgr.GetFieldIndexer()
	if err := fe.IndexField(context.Background(), &corev1.Pod{}, "status.phase", func(obj client.Object) []string {
		pod := obj.(*corev1.Pod)
		return []string{string(pod.Status.Phase)}
	}); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&chaosv1.Chaos{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}
