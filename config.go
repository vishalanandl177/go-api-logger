package apilog

import (
	"slices"
	"time"
)

type QueueConfig struct {
	Capacity, MaxBytes, BatchSize int
	FlushInterval, WriteTimeout   time.Duration
}
type ProfileConfig struct {
	Enabled    bool
	SampleRate float64
	MaxQueries int
}
type CorrelationConfig struct {
	Enabled                          bool
	RequestIDHeaders, TraceIDHeaders []string
	GenerateID                       func() string
}
type SecurityConfig struct {
	Enabled                         bool
	InspectRequest, InspectResponse bool
	SampleBytes, MaxActors          int
	Window                          time.Duration
	Rules                           []string
	Secret                          []byte
}
type Decision struct {
	Skip, StripHeaders, StripRequest, StripResponse bool
	DisableStorage, DisableExport                   bool
	StripQuery                                      bool
	MaskKeys                                        []string
}

func cloneConfig(c Config) Config {
	c.Outputs = slices.Clone(c.Outputs)
	c.MaskKeys = slices.Clone(c.MaskKeys)
	c.Methods = slices.Clone(c.Methods)
	c.SkipRoutes = slices.Clone(c.SkipRoutes)
	c.SkipNames = slices.Clone(c.SkipNames)
	c.SkipGroups = slices.Clone(c.SkipGroups)
	c.SkipPaths = slices.Clone(c.SkipPaths)
	c.Statuses = slices.Clone(c.Statuses)
	c.ContentTypes = slices.Clone(c.ContentTypes)
	c.TrustedProxies = slices.Clone(c.TrustedProxies)
	c.Rules = slices.Clone(c.Rules)
	for i := range c.Rules {
		r := &c.Rules[i]
		r.Methods = slices.Clone(r.Methods)
		r.Statuses = slices.Clone(r.Statuses)
		r.StatusClasses = slices.Clone(r.StatusClasses)
		r.Decision.MaskKeys = slices.Clone(r.Decision.MaskKeys)
	}
	c.Correlation.RequestIDHeaders = slices.Clone(c.Correlation.RequestIDHeaders)
	c.Correlation.TraceIDHeaders = slices.Clone(c.Correlation.TraceIDHeaders)
	c.Security.Rules = slices.Clone(c.Security.Rules)
	c.Security.Secret = slices.Clone(c.Security.Secret)
	return c
}

type Rule struct {
	Route, Name, Group, Handler, PathPrefix string
	Methods                                 []string
	Statuses                                []int
	StatusClasses                           []int
	Decision                                Decision
}
type Sanitizer func(contentType string, data []byte, maskKeys []string) ([]byte, error)
type Config struct {
	Outputs                                                         []Output
	Queue                                                           QueueConfig
	RequestBodyLimit, ResponseBodyLimit                             int
	MetadataOnly                                                    bool
	MaskKeys, Methods, SkipRoutes, SkipNames, SkipGroups, SkipPaths []string
	Statuses                                                        []int
	ContentTypes                                                    []string
	PathMode                                                        string
	TrustedProxies                                                  []string
	Rules                                                           []Rule
	Policy                                                          func(Event) (Decision, error)
	Transform                                                       func(Event) (*Event, error)
	Sanitizer                                                       Sanitizer
	Profile                                                         ProfileConfig
	Correlation                                                     CorrelationConfig
	Security                                                        SecurityConfig
	Observer                                                        Observer
	SlowThreshold                                                   time.Duration
}

func DefaultConfig() Config {
	return Config{
		Queue:            QueueConfig{Capacity: 1024, MaxBytes: 16 << 20, BatchSize: 50, FlushInterval: 10 * time.Second, WriteTimeout: 5 * time.Second},
		RequestBodyLimit: 32 << 10, ResponseBodyLimit: 64 << 10,
		ContentTypes: []string{"application/json", "application/*+json"}, PathMode: "full",
		Profile:       ProfileConfig{SampleRate: 1, MaxQueries: 1000},
		Correlation:   CorrelationConfig{RequestIDHeaders: []string{"X-Request-ID", "X-Correlation-ID"}, TraceIDHeaders: []string{"traceparent", "X-Trace-ID"}},
		Security:      SecurityConfig{SampleBytes: 8192, MaxActors: 4096, Window: 5 * time.Minute},
		SlowThreshold: 200 * time.Millisecond,
	}
}
