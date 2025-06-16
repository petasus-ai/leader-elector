package lib

import (
	"context"
	"os"
	"time"

	"k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
)

func getCurrentLeader(electionId, namespace string, c kubernetes.Interface) (string, *v1.Lease, error) {
	lease, err := c.CoordinationV1().Leases(namespace).Get(context.Background(), electionId, metav1.GetOptions{})
	if err != nil {
		return "", nil, err
	}
	if lease.Spec.HolderIdentity == nil {
		return "", lease, nil
	}
	return *lease.Spec.HolderIdentity, lease, nil
}

// NewSimpleElection creates an election, it defaults namespace to 'default' and ttl to 10s
func NewSimpleElection(electionId, id string, callback func(leader string), c kubernetes.Interface) (*leaderelection.LeaderElector, error) {
	return NewElection(electionId, id, metav1.NamespaceDefault, 10*time.Second, callback, c)
}

// NewElection creates an election. 'namespace'/'electionId' should be an existing Kubernetes resource
// 'id' is the id of this leader, should be unique.
func NewElection(electionId, id, namespace string, ttl time.Duration, callback func(leader string), c kubernetes.Interface) (*leaderelection.LeaderElector, error) {
	// Check or create Lease resource
	_, err := c.CoordinationV1().Leases(namespace).Get(context.Background(), electionId, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			_, err = c.CoordinationV1().Leases(namespace).Create(context.Background(), &v1.Lease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      electionId,
					Namespace: namespace,
				},
			}, metav1.CreateOptions{})
			if err != nil && !errors.IsAlreadyExists(err) {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	leader, _, err := getCurrentLeader(electionId, namespace, c)
	if err != nil {
		return nil, err
	}
	callback(leader)

	// Set up event recorder
	broadcaster := record.NewBroadcaster()
	broadcaster.StartLogging(klog.V(3).Infof)
	broadcaster.StartEventWatcher(func(event *corev1.Event) {
		_, err := c.CoreV1().Events(namespace).Create(context.Background(), event, metav1.CreateOptions{})
		if err != nil && !errors.IsAlreadyExists(err) {
			klog.Errorf("Failed to create event: %v", err)
		}
	})
	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	recorder := broadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{
		Component: "leader-elector",
		Host:      hostname,
	})

	// Set up lease lock
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      electionId,
			Namespace: namespace,
		},
		Client: c.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity:      id,
			EventRecorder: recorder,
		},
	}

	// Leader election callbacks
	callbacks := leaderelection.LeaderCallbacks{
		OnStartedLeading: func(ctx context.Context) {
			callback(id)
		},
		OnStoppedLeading: func() {
			leader, _, err := getCurrentLeader(electionId, namespace, c)
			if err != nil {
				klog.Errorf("failed to get leader: %v", err)
				callback("")
				return
			}
			callback(leader)
		},
		OnNewLeader: func(identity string) {
			callback(identity)
		},
	}

	// Leader election config
	config := leaderelection.LeaderElectionConfig{
		Lock:          lock,
		LeaseDuration: ttl,
		RenewDeadline: ttl / 2,
		RetryPeriod:   ttl / 4,
		Callbacks:     callbacks,
	}

	return leaderelection.NewLeaderElector(config)
}

// RunElection runs an election given a leader elector. Doesn't return.
func RunElection(e *leaderelection.LeaderElector) {
	e.Run(context.Background())
}
