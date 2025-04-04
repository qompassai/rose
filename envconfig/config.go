package envconfig

import (
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Host returns the scheme and host. Host can be configured via the ROSE_HOST environment variable.
// Default is scheme "http" and host "127.0.0.1:11434"
func Host() *url.URL {
	defaultPort := "11434"

	s := strings.TrimSpace(Var("ROSE_HOST"))
	scheme, hostport, ok := strings.Cut(s, "://")
	switch {
	case !ok:
		scheme, hostport = "http", s
	case scheme == "http":
		defaultPort = "80"
	case scheme == "https":
		defaultPort = "443"
	}

	hostport, path, _ := strings.Cut(hostport, "/")
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		host, port = "127.0.0.1", defaultPort
		if ip := net.ParseIP(strings.Trim(hostport, "[]")); ip != nil {
			host = ip.String()
		} else if hostport != "" {
			host = hostport
		}
	}

	if n, err := strconv.ParseInt(port, 10, 32); err != nil || n > 65535 || n < 0 {
		slog.Warn("invalid port, using default", "port", port, "default", defaultPort)
		port = defaultPort
	}

	return &url.URL{
		Scheme: scheme,
		Host:   net.JoinHostPort(host, port),
		Path:   path,
	}
}

// AllowedOrigins returns a list of allowed origins. AllowedOrigins can be configured via the ROSE_ORIGINS environment variable.
func AllowedOrigins() (origins []string) {
	if s := Var("ROSE_ORIGINS"); s != "" {
		origins = strings.Split(s, ",")
	}

	for _, origin := range []string{"localhost", "127.0.0.1", "0.0.0.0"} {
		origins = append(origins,
			fmt.Sprintf("http://%s", origin),
			fmt.Sprintf("https://%s", origin),
			fmt.Sprintf("http://%s", net.JoinHostPort(origin, "*")),
			fmt.Sprintf("https://%s", net.JoinHostPort(origin, "*")),
		)
	}

	origins = append(origins,
		"app://*",
		"file://*",
		"tauri://*",
		"vscode-webview://*",
		"vscode-file://*",
	)

	return origins
}

// Models returns the path to the models directory. Models directory can be configured via the ROSE_MODELS environment variable.
// Default is $HOME/.rose/models
func Models() string {
	if s := Var("ROSE_MODELS"); s != "" {
		return s
	}

	home, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}

	return filepath.Join(home, ".rose", "models")
}

// KeepAlive returns the duration that models stay loaded in memory. KeepAlive can be configured via the ROSE_KEEP_ALIVE environment variable.
// Negative values are treated as infinite. Zero is treated as no keep alive.
// Default is 5 minutes.
func KeepAlive() (keepAlive time.Duration) {
	keepAlive = 5 * time.Minute
	if s := Var("ROSE_KEEP_ALIVE"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			keepAlive = d
		} else if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			keepAlive = time.Duration(n) * time.Second
		}
	}

	if keepAlive < 0 {
		return time.Duration(math.MaxInt64)
	}

	return keepAlive
}

// LoadTimeout returns the duration for stall detection during model loads. LoadTimeout can be configured via the ROSE_LOAD_TIMEOUT environment variable.
// Zero or Negative values are treated as infinite.
// Default is 5 minutes.
func LoadTimeout() (loadTimeout time.Duration) {
	loadTimeout = 5 * time.Minute
	if s := Var("ROSE_LOAD_TIMEOUT"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			loadTimeout = d
		} else if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			loadTimeout = time.Duration(n) * time.Second
		}
	}

	if loadTimeout <= 0 {
		return time.Duration(math.MaxInt64)
	}

	return loadTimeout
}

func Bool(k string) func() bool {
	return func() bool {
		if s := Var(k); s != "" {
			b, err := strconv.ParseBool(s)
			if err != nil {
				return true
			}

			return b
		}

		return false
	}
}

var (
	// Debug enabled additional debug information.
	Debug = Bool("ROSE_DEBUG")
	// FlashAttention enables the experimental flash attention feature.
	FlashAttention = Bool("ROSE_FLASH_ATTENTION")
	// KvCacheType is the quantization type for the K/V cache.
	KvCacheType = String("ROSE_KV_CACHE_TYPE")
	// NoHistory disables readline history.
	NoHistory = Bool("ROSE_NOHISTORY")
	// NoPrune disables pruning of model blobs on startup.
	NoPrune = Bool("ROSE_NOPRUNE")
	// SchedSpread allows scheduling models across all GPUs.
	SchedSpread = Bool("ROSE_SCHED_SPREAD")
	// IntelGPU enables experimental Intel GPU detection.
	IntelGPU = Bool("ROSE_INTEL_GPU")
	// MultiUserCache optimizes prompt caching for multi-user scenarios
	MultiUserCache = Bool("ROSE_MULTIUSER_CACHE")
	// Enable the new Rose engine
	NewEngine = Bool("ROSE_NEW_ENGINE")
	// ContextLength sets the default context length
	ContextLength = Uint("ROSE_CONTEXT_LENGTH", 2048)
)

func String(s string) func() string {
	return func() string {
		return Var(s)
	}
}

var (
	LLMLibrary = String("ROSE_LLM_LIBRARY")

	CudaVisibleDevices    = String("CUDA_VISIBLE_DEVICES")
	HipVisibleDevices     = String("HIP_VISIBLE_DEVICES")
	RocrVisibleDevices    = String("ROCR_VISIBLE_DEVICES")
	GpuDeviceOrdinal      = String("GPU_DEVICE_ORDINAL")
	HsaOverrideGfxVersion = String("HSA_OVERRIDE_GFX_VERSION")
)

func Uint(key string, defaultValue uint) func() uint {
	return func() uint {
		if s := Var(key); s != "" {
			if n, err := strconv.ParseUint(s, 10, 64); err != nil {
				slog.Warn("invalid environment variable, using default", "key", key, "value", s, "default", defaultValue)
			} else {
				return uint(n)
			}
		}

		return defaultValue
	}
}

var (
	// NumParallel sets the number of parallel model requests. NumParallel can be configured via the ROSE_NUM_PARALLEL environment variable.
	NumParallel = Uint("ROSE_NUM_PARALLEL", 0)
	// MaxRunners sets the maximum number of loaded models. MaxRunners can be configured via the ROSE_MAX_LOADED_MODELS environment variable.
	MaxRunners = Uint("ROSE_MAX_LOADED_MODELS", 0)
	// MaxQueue sets the maximum number of queued requests. MaxQueue can be configured via the ROSE_MAX_QUEUE environment variable.
	MaxQueue = Uint("ROSE_MAX_QUEUE", 512)
	// MaxVRAM sets a maximum VRAM override in bytes. MaxVRAM can be configured via the ROSE_MAX_VRAM environment variable.
	MaxVRAM = Uint("ROSE_MAX_VRAM", 0)
)

func Uint64(key string, defaultValue uint64) func() uint64 {
	return func() uint64 {
		if s := Var(key); s != "" {
			if n, err := strconv.ParseUint(s, 10, 64); err != nil {
				slog.Warn("invalid environment variable, using default", "key", key, "value", s, "default", defaultValue)
			} else {
				return n
			}
		}

		return defaultValue
	}
}

// Set aside VRAM per GPU
var GpuOverhead = Uint64("ROSE_GPU_OVERHEAD", 0)

type EnvVar struct {
	Name        string
	Value       any
	Description string
}

func AsMap() map[string]EnvVar {
	ret := map[string]EnvVar{
		"ROSE_DEBUG":             {"ROSE_DEBUG", Debug(), "Show additional debug information (e.g. ROSE_DEBUG=1)"},
		"ROSE_FLASH_ATTENTION":   {"ROSE_FLASH_ATTENTION", FlashAttention(), "Enabled flash attention"},
		"ROSE_KV_CACHE_TYPE":     {"ROSE_KV_CACHE_TYPE", KvCacheType(), "Quantization type for the K/V cache (default: f16)"},
		"ROSE_GPU_OVERHEAD":      {"ROSE_GPU_OVERHEAD", GpuOverhead(), "Reserve a portion of VRAM per GPU (bytes)"},
		"ROSE_HOST":              {"ROSE_HOST", Host(), "IP Address for the rose server (default 127.0.0.1:11434)"},
		"ROSE_KEEP_ALIVE":        {"ROSE_KEEP_ALIVE", KeepAlive(), "The duration that models stay loaded in memory (default \"5m\")"},
		"ROSE_LLM_LIBRARY":       {"ROSE_LLM_LIBRARY", LLMLibrary(), "Set LLM library to bypass autodetection"},
		"ROSE_LOAD_TIMEOUT":      {"ROSE_LOAD_TIMEOUT", LoadTimeout(), "How long to allow model loads to stall before giving up (default \"5m\")"},
		"ROSE_MAX_LOADED_MODELS": {"ROSE_MAX_LOADED_MODELS", MaxRunners(), "Maximum number of loaded models per GPU"},
		"ROSE_MAX_QUEUE":         {"ROSE_MAX_QUEUE", MaxQueue(), "Maximum number of queued requests"},
		"ROSE_MODELS":            {"ROSE_MODELS", Models(), "The path to the models directory"},
		"ROSE_NOHISTORY":         {"ROSE_NOHISTORY", NoHistory(), "Do not preserve readline history"},
		"ROSE_NOPRUNE":           {"ROSE_NOPRUNE", NoPrune(), "Do not prune model blobs on startup"},
		"ROSE_NUM_PARALLEL":      {"ROSE_NUM_PARALLEL", NumParallel(), "Maximum number of parallel requests"},
		"ROSE_ORIGINS":           {"ROSE_ORIGINS", AllowedOrigins(), "A comma separated list of allowed origins"},
		"ROSE_SCHED_SPREAD":      {"ROSE_SCHED_SPREAD", SchedSpread(), "Always schedule model across all GPUs"},
		"ROSE_MULTIUSER_CACHE":   {"ROSE_MULTIUSER_CACHE", MultiUserCache(), "Optimize prompt caching for multi-user scenarios"},
		"ROSE_CONTEXT_LENGTH":    {"ROSE_CONTEXT_LENGTH", ContextLength(), "Context length to use unless otherwise specified (default: 2048)"},
		"ROSE_NEW_ENGINE":        {"ROSE_NEW_ENGINE", NewEngine(), "Enable the new Rose engine"},

		// Informational
		"HTTP_PROXY":  {"HTTP_PROXY", String("HTTP_PROXY")(), "HTTP proxy"},
		"HTTPS_PROXY": {"HTTPS_PROXY", String("HTTPS_PROXY")(), "HTTPS proxy"},
		"NO_PROXY":    {"NO_PROXY", String("NO_PROXY")(), "No proxy"},
	}

	if runtime.GOOS != "windows" {
		// Windows environment variables are case-insensitive so there's no need to duplicate them
		ret["http_proxy"] = EnvVar{"http_proxy", String("http_proxy")(), "HTTP proxy"}
		ret["https_proxy"] = EnvVar{"https_proxy", String("https_proxy")(), "HTTPS proxy"}
		ret["no_proxy"] = EnvVar{"no_proxy", String("no_proxy")(), "No proxy"}
	}

	if runtime.GOOS != "darwin" {
		ret["CUDA_VISIBLE_DEVICES"] = EnvVar{"CUDA_VISIBLE_DEVICES", CudaVisibleDevices(), "Set which NVIDIA devices are visible"}
		ret["HIP_VISIBLE_DEVICES"] = EnvVar{"HIP_VISIBLE_DEVICES", HipVisibleDevices(), "Set which AMD devices are visible by numeric ID"}
		ret["ROCR_VISIBLE_DEVICES"] = EnvVar{"ROCR_VISIBLE_DEVICES", RocrVisibleDevices(), "Set which AMD devices are visible by UUID or numeric ID"}
		ret["GPU_DEVICE_ORDINAL"] = EnvVar{"GPU_DEVICE_ORDINAL", GpuDeviceOrdinal(), "Set which AMD devices are visible by numeric ID"}
		ret["HSA_OVERRIDE_GFX_VERSION"] = EnvVar{"HSA_OVERRIDE_GFX_VERSION", HsaOverrideGfxVersion(), "Override the gfx used for all detected AMD GPUs"}
		ret["ROSE_INTEL_GPU"] = EnvVar{"ROSE_INTEL_GPU", IntelGPU(), "Enable experimental Intel GPU detection"}
	}

	return ret
}

func Values() map[string]string {
	vals := make(map[string]string)
	for k, v := range AsMap() {
		vals[k] = fmt.Sprintf("%v", v.Value)
	}
	return vals
}

// Var returns an environment variable stripped of leading and trailing quotes or spaces
func Var(key string) string {
	return strings.Trim(strings.TrimSpace(os.Getenv(key)), "\"'")
}
