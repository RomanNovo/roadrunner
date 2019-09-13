package metrics

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spiral/roadrunner/service/rpc"
)

// ID declares public service name.
const ID = "metrics"

// Service to manage application metrics using Prometheus.
type Service struct {
	cfg        *Config
	mu         sync.Mutex
	http       *http.Server
	mdlw       []http.Handler
	collectors sync.Map
	registry   *prometheus.Registry
}

// Init service.
func (s *Service) Init(cfg *Config, r *rpc.Service) (bool, error) {
	s.cfg = cfg
	s.registry = prometheus.NewRegistry()

	s.registry.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	s.registry.MustRegister(prometheus.NewGoCollector())

	if r != nil {
		if err := r.Register(ID, &rpcServer{s}); err != nil {
			return false, err
		}
	}

	return true, nil
}

// Enabled indicates that server is able to collect metrics.
func (s *Service) Enabled() bool {
	return s.cfg != nil
}

// Register new prometheus collector.
func (s *Service) Register(c prometheus.Collector) error {
	return s.registry.Register(c)
}

// MustRegister registers new collector or fails with panic.
func (s *Service) MustRegister(c prometheus.Collector) {
	s.registry.MustRegister(c)
}

// Serve prometheus metrics service.
func (s *Service) Serve() error {
	// register application specific metrics
	collectors, err := s.cfg.getCollectors()
	if err != nil {
		return err
	}

	for name, collector := range collectors {
		if err := s.registry.Register(collector); err != nil {
			return err
		}

		s.collectors.Store(name, collector)
	}

	s.mu.Lock()

	handler := promhttp.HandlerFor(
		s.registry,
		promhttp.HandlerOpts{},
	)
	http.Handle("/", s.BasicAuth(handler))
	s.http = &http.Server{Addr: s.cfg.Address}
	s.mu.Unlock()

	return s.http.ListenAndServe()
}

// BasicAuth for prometheus metrics
func (s *Service) BasicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Our middleware logic goes here...

		w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)

		s := strings.SplitN(r.Header.Get("Authorization"), " ", 2)
		if len(s) != 2 {
			http.Error(w, "Not authorized", 401)
			return
		}

		b, err := base64.StdEncoding.DecodeString(s[1])
		if err != nil {
			http.Error(w, err.Error(), 401)
			return
		}

		pair := strings.SplitN(string(b), ":", 2)
		if len(pair) != 2 {
			http.Error(w, "Not authorized", 401)
			return
		}

		if pair[0] != "username" || pair[1] != "password" {
			http.Error(w, "Not authorized", 401)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Stop prometheus metrics service.
func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.http != nil {
		// gracefully stop server
		go s.http.Shutdown(context.Background())
	}
}

// Collector returns application specific collector by name or nil if collector not found.
func (s *Service) Collector(name string) prometheus.Collector {
	collector, ok := s.collectors.Load(name)
	if !ok {
		return nil
	}

	return collector.(prometheus.Collector)
}
