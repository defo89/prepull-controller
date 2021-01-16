/*


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

package controllers

import (
	"context"
	"fmt"
	prepullv1 "github.com/defo89/prepull-controller/api/v1"
	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// ImagePullReconciler reconciles a ImagePull object
type ImagePullReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=prepull.cluster.local,resources=imagepulls,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=prepull.cluster.local,resources=imagepulls/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=prepull.cluster.local,resources=imagepulls/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list

func (r *ImagePullReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("imagepull", req.NamespacedName)

	puller := types.NamespacedName{
		Namespace: req.Namespace,
		Name:      "imagepull",
	}

	// Fetch the ImagePull instance
	imagepuller := &prepullv1.ImagePull{}
	//err := r.Get(ctx, req.NamespacedName, imagepuller)
	err := r.Get(ctx, puller, imagepuller)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			log.Info("ImagePull resource not found. Ignoring")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get ImagePull")
		return ctrl.Result{}, err
	}

	namespace := string(req.Namespace)
	podList := &corev1.PodList{}

	//namespace := prepullv1.ImagePullSpec
	err = r.List(context.Background(), podList, client.InNamespace(namespace))
	if err != nil {
		log.Error(err, "failed to list pods")
	}

	log.Info("Pods in the cluster", "Namespace: ", namespace, "Count: ", len(podList.Items))

	var imageListFull []string

	for _, podInfo := range (*podList).Items {
		//log.Printf("pods-name=%v\n", podInfo.Name)
		for _, containerInfo := range (podInfo).Spec.Containers {
			//log.Printf("container-name=%v\n", containerInfo.Image)
			imageListFull = append(imageListFull, string(containerInfo.Image))
		}
	}

	if len(imageListFull) == 0 {
		log.Info("No pods found in namespace. Ignoring")
		return ctrl.Result{}, nil
	}

	imageList := unique(imageListFull)

	log.Info("Listing images", "Images", imageList)

	var containerList []corev1.Container
	for i, image := range imageList {
		imageName := fmt.Sprintf("image-%d", i)
		containerList = append(containerList, corev1.Container{
			Image:   image,
			Name:    imageName,
			Command: []string{"/bin/sh"},
			Args:    []string{"-c", "while true; do echo hello; sleep 10;done"},
		})
	}

	// Check if the DaemonSet already exists, if not create a new one
	found := &appsv1.DaemonSet{}
	//fmt.Println("one", found)
	err = r.Get(ctx, types.NamespacedName{Name: imagepuller.Name, Namespace: imagepuller.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		// Define a new daemonset
		ds := r.daemonsetForImagePull(imagepuller, containerList)
		log.Info("Creating a new DaemonSet", "DaemonSet.Namespace", ds.Namespace, "DaemonSet.Name", ds.Name)
		err = r.Create(ctx, ds)
		if err != nil {
			log.Error(err, "Failed to create new DaemonSet", "Deployment.Namespace", ds.Namespace, "Deployment.Name", ds.Name)
			return ctrl.Result{}, err
		}
		// Daemonset created successfully - return and requeue
		//return ctrl.Result{Requeue: true}, nil
		return ctrl.Result{}, nil
	} else if err != nil {
		log.Error(err, "Failed to get DaemonSet")
		return ctrl.Result{}, err
	}
	if errors.IsAlreadyExists(err) {
		log.Info("Is present")
	}

	ds := r.daemonsetForImagePull(imagepuller, containerList)
	log.Info("Updating DaemonSet", "DaemonSet.Namespace", ds.Namespace, "DaemonSet.Name", ds.Name)
	err = r.Update(ctx, ds)
	if err != nil {
		log.Error(err, "Failed to create new DaemonSet", "Deployment.Namespace", ds.Namespace, "Deployment.Name", ds.Name)
		return ctrl.Result{}, err
	}
	// Daemonset updated successfully - return
	return ctrl.Result{}, nil

	//log.Info("End")
	//return ctrl.Result{}, nil
}

// daemonsetForImagePull returns a prepull DaemonSet object
func (r *ImagePullReconciler) daemonsetForImagePull(i *prepullv1.ImagePull, containerList []corev1.Container) *appsv1.DaemonSet {
	ls := labelsForImagePull(i.Name)

	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      i.Name,
			Namespace: i.Namespace,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					Tolerations: []corev1.Toleration{{
						Operator: "Exists",
					}},
					Containers: containerList,
				},
			},
		},
	}
	// Set ImagePull instance as the owner and controller
	ctrl.SetControllerReference(i, ds, r.Scheme)
	return ds
}

// labelsForImagePull returns the labels for selecting the resources
// belonging to the given ImagePull CR name.
func labelsForImagePull(name string) map[string]string {
	return map[string]string{"app": "imagepull", "imagepull_cr": name}
}

// unique removes duplicate values from slice of strings
func unique(stringSlice []string) []string {
	keys := make(map[string]bool)
	list := []string{}
	for _, entry := range stringSlice {
		if _, value := keys[entry]; !value {
			keys[entry] = true
			list = append(list, entry)
		}
	}
	return list
}

func (r *ImagePullReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&prepullv1.ImagePull{}).
		//For(&appsv1.Deployment{}).
		//For(&corev1.Pod{}).
		Watches(&source.Kind{Type: &appsv1.Deployment{}}, &handler.EnqueueRequestForObject{}).
		//Owns(&appsv1.DaemonSet{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(r)
}
