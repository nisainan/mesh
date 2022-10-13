package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"gitlab.oneitfarm.com/bifrost/sesdk/discovery"
	kubeerror "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	listers "k8s.io/client-go/listers/core/v1"
)

// API is an implementation of an api.
type API struct {
	http.Server

	//readiness     *safe.Safe
	//configuration *safe.Safe
	//topology      *safe.Safe

	namespace string
	podLister listers.PodLister
	log       logrus.FieldLogger
	discover  *discovery.Discovery
}

type podInfo struct {
	Name  string
	IP    string
	Ready bool
}

// NewAPI creates a new api.
func NewAPI(log logrus.FieldLogger, port int32, host string, namespace string) (*API, error) {

	router := mux.NewRouter()

	api := &API{
		Server: http.Server{
			Addr:         fmt.Sprintf("%s:%d", host, port),
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 5 * time.Second,
			Handler:      router,
		},
		//configuration: safe.New(provider.NewDefaultDynamicConfig()),
		//topology:      safe.New(topology.NewTopology()),
		//readiness:     safe.New(false),
		//podLister:     podLister,
		namespace: namespace,
		log:       log,
	}

	//router.HandleFunc("/api/configuration/current", api.getCurrentConfiguration)
	//router.HandleFunc("/api/topology/current", api.getCurrentTopology)
	router.HandleFunc("/api/topology/pods", api.getCurrentPods)
	router.HandleFunc("/api/status/nodes", api.getMeshNodes)
	//router.HandleFunc("/api/status/node/{node}/configuration", api.getMeshNodeConfiguration)
	//router.HandleFunc("/api/status/readiness", api.getReadiness)

	return api, nil
}

func (a *API) SetPodLister(podLister listers.PodLister) {
	a.podLister = podLister
}

// SetReadiness sets the readiness flag in the API.
//func (a *API) SetReadiness(isReady bool) {
//	a.readiness.Set(isReady)
//	a.log.Debugf("API readiness: %t", isReady)
//}
//
//// SetConfig sets the current dynamic configuration.
//func (a *API) SetConfig(cfg *dynamic.Configuration) {
//	a.configuration.Set(cfg)
//}
//
//// SetTopology sets the current topology.
//func (a *API) SetTopology(topo *topology.Topology) {
//	a.topology.Set(topo)
//}

// getCurrentConfiguration returns the current configuration.
//func (a *API) getCurrentConfiguration(w http.ResponseWriter, _ *http.Request) {
//	w.Header().Set("Content-Type", "application/json")
//
//	if err := json.NewEncoder(w).Encode(a.configuration.Get()); err != nil {
//		a.log.Errorf("Unable to serialize dynamic configuration: %v", err)
//		http.Error(w, "", http.StatusInternalServerError)
//	}
//}
//
//// getCurrentTopology returns the current topology.
//func (a *API) getCurrentTopology(w http.ResponseWriter, _ *http.Request) {
//	w.Header().Set("Content-Type", "application/json")
//
//	if err := json.NewEncoder(w).Encode(a.topology.Get()); err != nil {
//		a.log.Errorf("Unable to serialize topology: %v", err)
//		http.Error(w, "", http.StatusInternalServerError)
//	}
//}

// getCurrentPods returns the current pods.
func (a *API) getCurrentPods(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	podList, err := a.podLister.List(labels.Everything())
	if err != nil {
		a.log.Errorf("Unable to retrieve pod list: %v", err)
		http.Error(w, "", http.StatusInternalServerError)

		return
	}

	podInfoList := make([]podInfo, len(podList))
	for i, pod := range podList {
		readiness := true

		for _, status := range pod.Status.ContainerStatuses {
			if !status.Ready {
				// If there is a non-ready container, pod is not ready.
				readiness = false
				break
			}
		}

		podInfoList[i] = podInfo{
			Name:  pod.Name,
			IP:    pod.Status.PodIP,
			Ready: readiness,
		}
	}

	if err := json.NewEncoder(w).Encode(podInfoList); err != nil {
		a.log.Errorf("Unable to serialize pod list: %v", err)
		http.Error(w, "", http.StatusInternalServerError)
	}
}

// getReadiness returns the current readiness value, and sets the status code to 500 if not ready.
//func (a *API) getReadiness(w http.ResponseWriter, _ *http.Request) {
//	isReady, _ := a.readiness.Get().(bool)
//	if !isReady {
//		http.Error(w, "", http.StatusInternalServerError)
//		return
//	}
//
//	w.Header().Set("Content-Type", "application/json")
//
//	if err := json.NewEncoder(w).Encode(isReady); err != nil {
//		a.log.Errorf("Unable to serialize readiness: %v", err)
//		http.Error(w, "", http.StatusInternalServerError)
//	}
//}

// getMeshNodes returns a list of mesh nodes visible from the controller, and some basic readiness info.
func (a *API) getMeshNodes(w http.ResponseWriter, _ *http.Request) {
	podList, err := a.podLister.List(labels.Everything())
	if err != nil {
		a.log.Errorf("Unable to retrieve pod list: %v", err)
		http.Error(w, "", http.StatusInternalServerError)

		return
	}

	podInfoList := make([]podInfo, len(podList))

	for i, pod := range podList {
		readiness := true

		for _, status := range pod.Status.ContainerStatuses {
			if !status.Ready {
				// If there is a non-ready container, pod is not ready.
				readiness = false
				break
			}
		}

		podInfoList[i] = podInfo{
			Name:  pod.Name,
			IP:    pod.Status.PodIP,
			Ready: readiness,
		}
	}

	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(podInfoList); err != nil {
		a.log.Errorf("Unable to serialize mesh nodes: %v", err)
		http.Error(w, "", http.StatusInternalServerError)
	}
}

// getMeshNodeConfiguration returns the configuration for a named pod.
func (a *API) getMeshNodeConfiguration(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)

	pod, err := a.podLister.Pods(a.namespace).Get(vars["node"])
	if err != nil {
		if kubeerror.IsNotFound(err) {
			http.Error(w, "", http.StatusNotFound)

			return
		}

		http.Error(w, "", http.StatusInternalServerError)

		return
	}

	resp, err := http.Get(fmt.Sprintf("http://%s/api/rawdata", net.JoinHostPort(pod.Status.PodIP, "8080")))
	if err != nil {
		a.log.Errorf("Unable to get configuration from pod %q: %v", pod.Name, err)
		http.Error(w, "", http.StatusBadGateway)

		return
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			a.log.Errorf("Unable to close response body: %v", closeErr)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		a.log.Errorf("Unable to get configuration response body from pod %q: %v", pod.Name, err)
		http.Error(w, "", http.StatusBadGateway)

		return
	}

	w.Header().Set("Content-Type", "application/json")

	if _, err := w.Write(body); err != nil {
		a.log.Errorf("Unable to write mesh nodes: %v", err)
		http.Error(w, "", http.StatusInternalServerError)
	}
}
