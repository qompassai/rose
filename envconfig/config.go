package envconfig

import (
	"encoding/json"
	"errors"
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
	"sync"
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
		if s == "ollama.com" {
			scheme, hostport = "https", "ollama.com:443"
		}
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

// ConnectableHost returns Host() with unspecified bind addresses (0.0.0.0, ::)
// replaced by the corresponding loopback address (127.0.0.1, ::1).
// Unspecified addresses are valid for binding a server socket but not for
// connecting as a client, which fails on Windows.
func ConnectableHost() *url.URL {
	u := Host()
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		return u
	}

	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		if ip.To4() != nil {
			host = "127.0.0.1"
		} else {
			host = "::1"
		}
		u.Host = net.JoinHostPort(host, port)
	}

	return u
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
// Default is $HOME/.rose/models, or $HOME/.ollama/models when that legacy
// stock-Ollama store already exists (see the implementation for rationale).
func Models() string {
	if s := Var("ROSE_MODELS"); s != "" {
		return s
	}

	home, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}

	// Rose's own store is ~/.rose/models, but a machine that already runs
	// stock Ollama keeps its library in ~/.ollama/models. Prefer the legacy
	// store when it exists so an installed library (including locally
	// created models such as phlow's hf-* specialists) is served in place
	// instead of presenting an empty store on first run.
	if legacy := filepath.Join(home, ".ollama", "models"); dirExists(legacy) {
		return legacy
	}

	return filepath.Join(home, ".rose", "models")
}

// dirExists reports whether path is an existing directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
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

func Remotes() []string {
	var r []string
	raw := strings.TrimSpace(Var("ROSE_REMOTES"))
	if raw == "" {
		r = []string{"ollama.com"}
	} else {
		r = strings.Split(raw, ",")
	}
	return r
}

func BoolWithDefault(k string) func(defaultValue bool) bool {
	return func(defaultValue bool) bool {
		if s := Var(k); s != "" {
			b, err := strconv.ParseBool(s)
			if err != nil {
				return true
			}

			return b
		}

		return defaultValue
	}
}

func Bool(k string) func() bool {
	withDefault := BoolWithDefault(k)
	return func() bool {
		return withDefault(false)
	}
}

// LogLevel returns the log level for the application.
// Values are 0 or false INFO (Default), 1 or true DEBUG, 2 TRACE
func LogLevel() slog.Level {
	level := slog.LevelInfo
	if s := Var("ROSE_DEBUG"); s != "" {
		if b, _ := strconv.ParseBool(s); b {
			level = slog.LevelDebug
		} else if i, _ := strconv.ParseInt(s, 10, 64); i != 0 {
			level = slog.Level(i * -4)
		}
	}

	return level
}

var (
	// FlashAttention enables the experimental flash attention feature.
	FlashAttention = BoolWithDefault("ROSE_FLASH_ATTENTION")
	// GoTemplate enables Modelfile TEMPLATE rendering when a model has one.
	GoTemplate = BoolWithDefault("ROSE_GO_TEMPLATE")
	// DebugLogRequests logs inference requests to disk for replay/debugging.
	DebugLogRequests = Bool("ROSE_DEBUG_LOG_REQUESTS")
	// KvCacheType is the quantization type for the K/V cache.
	KvCacheType = String("ROSE_KV_CACHE_TYPE")
	// NoHistory disables readline history.
	NoHistory = Bool("ROSE_NOHISTORY")
	// NoPrune disables pruning of model blobs on startup.
	NoPrune = Bool("ROSE_NOPRUNE")
	// SchedSpread allows scheduling models across all GPUs.
	SchedSpread = Bool("ROSE_SCHED_SPREAD")
	// ContextLength sets the default context length
	ContextLength = Uint("ROSE_CONTEXT_LENGTH", 0)
	// Auth enables authentication between the Rose client and server
	UseAuth = Bool("ROSE_AUTH")
	// EnableVulkan controls Vulkan backend discovery.
	EnableVulkan = BoolWithDefault("ROSE_VULKAN")
	// EnableIntegratedGPU controls whether integrated GPUs may be selected.
	EnableIntegratedGPU = BoolWithDefault("ROSE_IGPU_ENABLE")
	// NoCloudEnv checks the ROSE_NO_CLOUD environment variable.
	NoCloudEnv = Bool("ROSE_NO_CLOUD")
	// CreateRemote forces model creation through the server API even when the server is local.
	CreateRemote = Bool("ROSE_CREATE_REMOTE")
)

func String(s string) func() string {
	return func() string {
		return Var(s)
	}
}

var (
	LLMLibrary = String("ROSE_LLM_LIBRARY")
	Editor     = String("ROSE_EDITOR")

	CudaVisibleDevices    = String("CUDA_VISIBLE_DEVICES")
	HipVisibleDevices     = String("HIP_VISIBLE_DEVICES")
	RocrVisibleDevices    = String("ROCR_VISIBLE_DEVICES")
	VkVisibleDevices      = String("GGML_VK_VISIBLE_DEVICES")
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
	NumParallel = Uint("ROSE_NUM_PARALLEL", 1)
	// MaxRunners sets the maximum number of loaded models. MaxRunners can be configured via the ROSE_MAX_LOADED_MODELS environment variable.
	MaxRunners = Uint("ROSE_MAX_LOADED_MODELS", 0)
	// MaxQueue sets the maximum number of queued requests. MaxQueue can be configured via the ROSE_MAX_QUEUE environment variable.
	MaxQueue = Uint("ROSE_MAX_QUEUE", 512)
	// MaxTransferStreams caps the number of simultaneous body-bearing
	// transfers during safetensors model pulls/pushes, keeping slower
	// networks from being saturated. Tune higher for fast networks. Has
	// no effect on GGUF transfers, which use the legacy upload/download
	// paths.
	MaxTransferStreams = Uint("ROSE_MAX_TRANSFER_STREAMS", 4)
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
		"ROSE_DEBUG":                {"ROSE_DEBUG", LogLevel(), "Show additional debug information (e.g. ROSE_DEBUG=1)"},
		"ROSE_DEBUG_LOG_REQUESTS":   {"ROSE_DEBUG_LOG_REQUESTS", DebugLogRequests(), "Log inference request bodies and replay curl commands to a temp directory"},
		"ROSE_GO_TEMPLATE":          {"ROSE_GO_TEMPLATE", GoTemplate(true), "Enable Modelfile TEMPLATE based rendering when available"},
		"ROSE_FLASH_ATTENTION":      {"ROSE_FLASH_ATTENTION", FlashAttention(false), "Enabled flash attention"},
		"ROSE_KV_CACHE_TYPE":        {"ROSE_KV_CACHE_TYPE", KvCacheType(), "Quantization type for the K/V cache (default: f16)"},
		"ROSE_GPU_OVERHEAD":         {"ROSE_GPU_OVERHEAD", GpuOverhead(), "Reserve a portion of VRAM per GPU (bytes)"},
		"ROSE_IGPU_ENABLE":          {"ROSE_IGPU_ENABLE", String("ROSE_IGPU_ENABLE")(), "Enable integrated GPUs"},
		"LLAMA_ARG_FIT":               {"LLAMA_ARG_FIT", String("LLAMA_ARG_FIT")(), "Enable llama.cpp automatic fit of unset memory options (default \"on\")"},
		"LLAMA_ARG_FIT_TARGET":        {"LLAMA_ARG_FIT_TARGET", String("LLAMA_ARG_FIT_TARGET")(), "Target free VRAM margin per device for llama.cpp fit (MiB)"},
		"ROSE_HOST":                 {"ROSE_HOST", Host(), "IP Address for the rose server (default 127.0.0.1:11434)"},
		"ROSE_KEEP_ALIVE":           {"ROSE_KEEP_ALIVE", KeepAlive(), "The duration that models stay loaded in memory (default \"5m\")"},
		"ROSE_LLM_LIBRARY":          {"ROSE_LLM_LIBRARY", LLMLibrary(), "Set LLM library to bypass autodetection"},
		"ROSE_LOAD_TIMEOUT":         {"ROSE_LOAD_TIMEOUT", LoadTimeout(), "How long to allow model loads to stall before giving up (default \"5m\")"},
		"ROSE_MAX_LOADED_MODELS":    {"ROSE_MAX_LOADED_MODELS", MaxRunners(), "Maximum number of loaded models per GPU"},
		"ROSE_MAX_TRANSFER_STREAMS": {"ROSE_MAX_TRANSFER_STREAMS", MaxTransferStreams(), "Maximum parallel transfer streams for safetensors model pulls/pushes (default 4)"},
		"ROSE_MAX_QUEUE":            {"ROSE_MAX_QUEUE", MaxQueue(), "Maximum number of queued requests"},
		"ROSE_MODELS":               {"ROSE_MODELS", Models(), "The path to the models directory"},
		"ROSE_NO_CLOUD":             {"ROSE_NO_CLOUD", NoCloud(), "Disable Rose cloud features (remote inference and web search)"},
		"ROSE_NOHISTORY":            {"ROSE_NOHISTORY", NoHistory(), "Do not preserve readline history"},
		"ROSE_NOPRUNE":              {"ROSE_NOPRUNE", NoPrune(), "Do not prune model blobs on startup"},
		"ROSE_NUM_PARALLEL":         {"ROSE_NUM_PARALLEL", NumParallel(), "Maximum number of parallel requests"},
		"ROSE_ORIGINS":              {"ROSE_ORIGINS", AllowedOrigins(), "A comma separated list of allowed origins"},
		"ROSE_SCHED_SPREAD":         {"ROSE_SCHED_SPREAD", SchedSpread(), "Always schedule model across all GPUs"},
		"ROSE_CONTEXT_LENGTH":       {"ROSE_CONTEXT_LENGTH", ContextLength(), "Context length to use unless otherwise specified (default: 4k/32k/256k based on VRAM)"},
		"ROSE_CREATE_REMOTE":        {"ROSE_CREATE_REMOTE", CreateRemote(), "Force model creation through the server API even when the server is local"},
		"ROSE_EDITOR":               {"ROSE_EDITOR", Editor(), "Path to editor for interactive prompt editing (Ctrl+G)"},
		"ROSE_REMOTES":              {"ROSE_REMOTES", Remotes(), "Allowed hosts for remote models (default \"ollama.com\")"},

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
		ret["GGML_VK_VISIBLE_DEVICES"] = EnvVar{"GGML_VK_VISIBLE_DEVICES", VkVisibleDevices(), "Set which Vulkan devices are visible by numeric ID"}
		ret["GPU_DEVICE_ORDINAL"] = EnvVar{"GPU_DEVICE_ORDINAL", GpuDeviceOrdinal(), "Set which AMD devices are visible by numeric ID"}
		ret["HSA_OVERRIDE_GFX_VERSION"] = EnvVar{"HSA_OVERRIDE_GFX_VERSION", HsaOverrideGfxVersion(), "Override the gfx used for all detected AMD GPUs"}
		ret["ROSE_VULKAN"] = EnvVar{"ROSE_VULKAN", EnableVulkan(true), "Enable Vulkan support"}
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

// Var returns an environment variable stripped of leading and trailing quotes or spaces.
//
// Rose renamed its environment variables from the OLLAMA_ prefix to ROSE_.
// For a ROSE_ key that is unset (or set empty), Var falls back to the legacy
// OLLAMA_ twin so existing scripts, service units, and deployments that set
// OLLAMA_* keep working unchanged. When both are set, ROSE_ wins.
func Var(key string) string {
	if v := os.Getenv(key); v != "" {
		return strings.Trim(strings.TrimSpace(v), "\"'")
	}
	if twin, ok := strings.CutPrefix(key, "ROSE_"); ok {
		return strings.Trim(strings.TrimSpace(os.Getenv("OLLAMA_"+twin)), "\"'")
	}
	return ""
}

// serverConfigData holds the parsed fields from ~/.rose/server.json.
type serverConfigData struct {
	DisableOllamaCloud bool `json:"disable_ollama_cloud,omitempty"`
}

var (
	serverCfgMu     sync.RWMutex
	serverCfgLoaded bool
	serverCfg       serverConfigData
)

func loadServerConfig() {
	serverCfgMu.RLock()
	if serverCfgLoaded {
		serverCfgMu.RUnlock()
		return
	}
	serverCfgMu.RUnlock()

	cfg := serverConfigData{}
	home, err := os.UserHomeDir()
	if err == nil {
		path := filepath.Join(home, ".rose", "server.json")
		data, err := os.ReadFile(path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				slog.Debug("envconfig: could not read server config", "error", err)
			}
		} else if err := json.Unmarshal(data, &cfg); err != nil {
			slog.Debug("envconfig: could not parse server config", "error", err)
		}
	}

	serverCfgMu.Lock()
	defer serverCfgMu.Unlock()
	if serverCfgLoaded {
		return
	}
	serverCfg = cfg
	serverCfgLoaded = true
}

func cachedServerConfig() serverConfigData {
	serverCfgMu.RLock()
	defer serverCfgMu.RUnlock()
	return serverCfg
}

// ReloadServerConfig refreshes the cached ~/.rose/server.json settings.
func ReloadServerConfig() {
	serverCfgMu.Lock()
	serverCfgLoaded = false
	serverCfg = serverConfigData{}
	serverCfgMu.Unlock()

	loadServerConfig()
}

// NoCloud returns true if Rose cloud features are disabled,
// checking both the ROSE_NO_CLOUD environment variable and
// the disable_ollama_cloud field in ~/.rose/server.json.
func NoCloud() bool {
	if NoCloudEnv() {
		return true
	}
	loadServerConfig()
	return cachedServerConfig().DisableOllamaCloud
}

// NoCloudSource returns the source of the cloud-disabled decision.
// Returns "none", "env", "config", or "both".
func NoCloudSource() string {
	envDisabled := NoCloudEnv()
	loadServerConfig()
	configDisabled := cachedServerConfig().DisableOllamaCloud

	switch {
	case envDisabled && configDisabled:
		return "both"
	case envDisabled:
		return "env"
	case configDisabled:
		return "config"
	default:
		return "none"
	}
}
