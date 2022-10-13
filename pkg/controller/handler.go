package controller

import (
	"fmt"

	"github.com/traefik/mesh/pkg/dis"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

const (
	ActionAdd    = "Add"
	ActionDelete = "Delete"
)

type enqueueWorkHandler struct {
	logger    logrus.FieldLogger
	workQueue workqueue.RateLimitingInterface
	discover  *dis.Discover
}

// OnAdd is called when an object is added to the informers cache.
func (h *enqueueWorkHandler) OnAdd(obj interface{}) {
	// 注意，OnAdd并不代表已经成功，Pod状态可能是Pending
	pod, isPod := obj.(*corev1.Pod)
	if !isPod {
		return
	}
	// check pod status,only Running can be Registered
	if pod.Status.Phase == corev1.PodRunning {
		// register instance
		err := h.discover.Register(pod)
		if err != nil {
			h.enqueueWork(ActionAdd, obj)
		}
	}
}

// OnUpdate is called when an object is updated in the informers cache.
func (h *enqueueWorkHandler) OnUpdate(oldObj interface{}, newObj interface{}) {
	// 这边需要判断Pod的先后状态，只有是Running时，才能执行上线
	oldPod, okOld := oldObj.(*corev1.Pod)
	newPod, okNew := newObj.(*corev1.Pod)
	//fmt.Println(oldPod.GetName(), newPod.GetName())
	//fmt.Println(oldPod.Status.Phase, newPod.Status.Phase)
	//fmt.Println(oldPod.GetResourceVersion(), newPod.GetResourceVersion())
	// This is a resync event, no extra work is needed.
	if okOld && okNew && oldPod.GetResourceVersion() == newPod.GetResourceVersion() {
		return
	}
	if oldPod.Status.Phase == newPod.Status.Phase {
		return
	}
	if oldPod.Status.Phase == corev1.PodRunning && oldPod.Status.Phase != newPod.Status.Phase {
		// means we should cancel this pod
		err := h.discover.Cancel(newPod)
		if err != nil {
			h.enqueueWork(ActionDelete, newPod)
		}
		return
	}
	if newPod.Status.Phase == corev1.PodRunning && oldPod.Status.Phase != newPod.Status.Phase {
		// means we should register this pod
		err := h.discover.Register(newPod)
		if err != nil {
			h.enqueueWork(ActionAdd, newPod)
		}
		return
	}
}

// OnDelete is called when an object is removed from the informers cache.
func (h *enqueueWorkHandler) OnDelete(obj interface{}) {
	pod, isPod := obj.(*corev1.Pod)
	if !isPod {
		return
	}
	err := h.discover.Cancel(pod)
	if err != nil {
		h.enqueueWork(ActionDelete, pod)
	}
}

func (h *enqueueWorkHandler) enqueueWork(action string, obj interface{}) {
	if _, isPod := obj.(*corev1.Pod); !isPod {
		return
	}
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		h.logger.Errorf("Unable to create a work key for resource %#v", obj)
		return
	}
	h.workQueue.Add(fmt.Sprintf("%s:%s", action, key))
}

func (h *enqueueWorkHandler) RetryWork(action string, pod *corev1.Pod) {
	switch action {
	case "Add":
		h.OnAdd(pod)
	case "Delete":
		h.OnDelete(pod)
	default:

	}
}
