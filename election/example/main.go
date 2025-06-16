package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/petasus-ai/leader-elector/election/lib"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

var (
	election          = flag.String("election", "", "Name of the election")
	electionNamespace = flag.String("election-namespace", "", "Namespace where the election lease resides")
	id                = flag.String("id", "", "Unique ID for this candidate")
	httpAddr          = flag.String("http", "", "Address to serve HTTP status (e.g., :4040)")
	ttl               = flag.Duration("ttl", 10*time.Second, "TTL for the leader lease")
	kubeconfig        = flag.String("kubeconfig", "", "Path to kubeconfig file (optional)")
	leaderName        = ""
)

func main() {
	klog.InitFlags(nil)
	flag.Parse()

	if *election == "" {
		klog.Fatal("Missing required flag --election")
	}
	if *id == "" {
		klog.Fatal("Missing required flag --id")
	}
	if *electionNamespace == "" {
		klog.Fatal("Missing required flag --election-namespace")
	}

	klog.V(4).Infof("Environment variable KLOG_V is set to: %s", os.Getenv("KLOG_V"))

	var config *rest.Config
	var err error
	if *kubeconfig != "" {
		config, err = rest.InClusterConfig() // Simplified, use kubeconfig if needed
	} else {
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		klog.Fatalf("Failed to build Kubernetes config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Failed to create Kubernetes clientset: %v", err)
	}

	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	if *httpAddr != "" {
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprintf(w, "%s is alive\n", *id)
		})
		http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			if leaderName == "" {
				klog.V(4).Info("No leader set")
				http.Error(w, "No leader set", http.StatusInternalServerError)
				return
			}
			klog.V(4).Infof("Leader is %s", leaderName)
			_, _ = fmt.Fprintf(w, "Leader is %s\n", leaderName)
		})
		go func() {
			klog.V(4).Infof("Starting HTTP server on %s", *httpAddr)
			if err := http.ListenAndServe(*httpAddr, nil); err != nil {
				klog.Fatalf("Failed to start HTTP server on %s: %v", *httpAddr, err)
			}
		}()
	}

	leaderElector, err := lib.NewElection(*election, *id, *electionNamespace, *ttl, func(leader string) {
		leaderName = leader
		klog.V(4).Infof("Current leader: %s", leader)
	}, clientset)
	if err != nil {
		klog.Fatalf("Failed to create leader elector: %v", err)
	}

	go lib.RunElection(leaderElector)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	klog.Info("Received termination signal, shutting down")
	cancel()
}
