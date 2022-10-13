package dis

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
	"gitlab.oneitfarm.com/bifrost/sesdk"
	"gitlab.oneitfarm.com/bifrost/sesdk/discovery"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
)

type Discover struct {
	disClient map[string]*discovery.Discovery
	config    *discovery.Config
	requester *resty.Client
	sync.RWMutex
}

func NewDiscovery() *Discover {
	return &Discover{
		disClient: make(map[string]*discovery.Discovery),
		config:    newConfig(),
		requester: resty.New().SetTimeout(time.Second),
	}
}

func (d *Discover) Register(pod *corev1.Pod) (err error) {
	dis, ok := d.get(pod)
	if !ok {
		dis = d.Set(pod)
	}
	_, err = dis.Register(context.TODO(), d.instance(pod, false))
	return
}

func (d *Discover) Cancel(pod *corev1.Pod) (err error) {
	defer d.delete(pod)
	dis, ok := d.get(pod)
	if !ok {
		dis = d.Set(pod)
	}
	dis.Cancel(d.instance(pod, true).AppID)
	return
}

func (d *Discover) HealthCheckOrCancel(pod *corev1.Pod) (err error) {
	dis, ok := d.get(pod)
	if !ok {
		dis = d.Set(pod)
	}
	if ok, err = d.healthCheck(pod); err != nil || !ok {
		dis.Cancel(d.instance(pod, true).AppID)
	}
	d.delete(pod)
	return
}

func (d *Discover) get(pod *corev1.Pod) (dis *discovery.Discovery, ok bool) {
	key, _ := cache.MetaNamespaceKeyFunc(pod)
	d.RLock()
	defer d.RUnlock()
	dis, ok = d.disClient[key]
	return
}

func (d *Discover) Set(pod *corev1.Pod) (dis *discovery.Discovery) {
	key, _ := cache.MetaNamespaceKeyFunc(pod)
	d.Lock()
	defer d.Unlock()
	dis, err := discovery.New(d.config)
	if err != nil {
		return
	}
	d.disClient[key] = dis
	return
}

func (d *Discover) delete(pod *corev1.Pod) {
	key, _ := cache.MetaNamespaceKeyFunc(pod)
	d.RLock()
	disClient, ok := d.disClient[key]
	d.RUnlock()
	if !ok || disClient == nil {
		return
	}
	d.Lock()
	defer d.Unlock()
	delete(d.disClient, key)
}

func newConfig() *discovery.Config {
	node := os.Getenv("DISCOVERY_ADDRESS")
	if len(node) == 0 {
		node = "service-eye.msp:9443"
	}
	return &discovery.Config{
		Nodes:  strings.Split(node, ","),
		Zone:   os.Getenv("ZONE"),
		Env:    os.Getenv("ENV"),
		Region: os.Getenv("REGION"),
		//Host:     os.Getenv("ENV"),
		RenewGap: time.Second * 30,
		//TLSConfig: sc.DiscoveryTLSConfig,
	}
}

func (d *Discover) healthCheck(pod *corev1.Pod) (ok bool, err error) {
	// check pod is healthy or not
	addr := fmt.Sprintf("%s://%s:%d", "http", pod.Status.PodIP, 80)
	res, err := d.requester.R().Get(addr + "/healthcheck")
	if err != nil {
		return false, err
	}
	if res == nil {
		return false, errors.New("healthCheck response is nil")
	}
	if res != nil && res.StatusCode() == http.StatusOK {
		return false, nil
	} else {
		return true, nil
	}
}

// instance make up instance
func (d *Discover) instance(pod *corev1.Pod, noHealthCheck bool) *sesdk.Instance {
	status := discovery.InstanceStatusReceive
	for _, containerStatuses := range pod.Status.ContainerStatuses {
		if !containerStatuses.Ready {
			// If there is a non-ready container, pod is not ready.
			status = discovery.InstanceStatusNotReceive
			break
		}
	}
	if !noHealthCheck {
		if ok, err := d.healthCheck(pod); err == nil && ok {
			status = discovery.InstanceStatusReceive
		} else {
			status = discovery.InstanceStatusNotReceive
		}
	}

	instance := &sesdk.Instance{
		Addrs:    []string{fmt.Sprintf("%s://%s:%d", "http", pod.Status.PodIP, 80)},
		LastTs:   time.Now().Unix(),
		Hostname: pod.Name,
		Status:   int64(status),
	}
	var metadata = make(map[string]string)
	for _, container := range pod.Spec.Containers {
		for _, value := range container.Env {
			if value.Name == "IDG_CLUSTERUID" {
				instance.Zone = value.Value
			}
			if value.Name == "IDG_RUNTIME" {
				instance.Env = value.Value
				metadata["runtime"] = value.Value
			}
			if value.Name == "IDG_UNIQUEID" {
				instance.AppID = value.Value
			}
			if value.Name == "IDG_SITEUID" {
				instance.Region = value.Value
			}
			if value.Name == "IDG_VERSION" {
				instance.Version = value.Value
			}
			// metadata
			if value.Name == "IDG_SERVICE_NAME" {
				metadata["service_name"] = value.Value
			}
			if value.Name == "MSP_PROTOCOL_MODE" {
				metadata["mode"] = value.Value
			}
			if value.Name == "IDG_SERVICE_IMAGEURL" {
				metadata["service_image"] = value.Value
			}
			if value.Name == "IDG_SERVICE_GATEWAY_ADDR" {
				metadata["service_gateway_addr"] = value.Value
			}
			if value.Name == "IDG_WEIGHT" {
				metadata["weight"] = value.Value
			}
			// TODO cert_sn
			metadata["cert_sn"] = ""
			if len(metadata["weight"]) == 0 {
				metadata["weight"] = "10"
			}
		}
	}
	return instance
}
