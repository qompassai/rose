package envconfig

import (
	"fmt"
	"strings"
	"testing"
)

func TestLoggedEnvironmentRedactsCredentials(t *testing.T) {
	t.Setenv("ROSE_HOST", "http://user:SECRET@127.0.0.1:11434")
	t.Setenv("HTTP_PROXY", "http://user:SECRET@proxy.example:8080")
	t.Setenv("HTTPS_PROXY", "https://user:SECRET@proxy.example:8080")
	t.Setenv("http_proxy", "http://user:SECRET@proxy.example:8080")
	t.Setenv("ROSE_TLS_KEY", "-----BEGIN PRIVATE KEY-----\nSECRET\n-----END PRIVATE KEY-----")
	if strings.Contains(fmt.Sprint(Values()), "SECRET") {
		t.Fatal("loggable environment values contain credentials")
	}
}

func TestStrictHostURL(t *testing.T) {
	t.Setenv("ROSE_HOST", "")
	host, err := HostURL()
	if err != nil || host.String() != "http://127.0.0.1:11434" {
		t.Fatalf("default host=%v err=%v", host, err)
	}
	for _, value := range []string{"http://127.0.0.1:bad", "https://user:SECRET@example.com", "ftp://127.0.0.1"} {
		t.Setenv("ROSE_HOST", value)
		if _, err := HostURL(); err == nil {
			t.Errorf("accepted invalid ROSE_HOST %q", value)
		} else if strings.Contains(err.Error(), "SECRET") {
			t.Fatal("error included credentials")
		}
	}
}

func TestTLSFileEnvironmentContract(t *testing.T) {
	t.Setenv("ROSE_TLS_CERT", "/cert.pem")
	t.Setenv("ROSE_TLS_KEY", "/key.pem")
	t.Setenv("ROSE_TLS_CLIENT_CA", "/client-ca.pem")
	t.Setenv("ROSE_TLS_CA", "/server-ca.pem")
	files := TLSFiles()
	if files.CertFile != "/cert.pem" || files.KeyFile != "/key.pem" ||
		files.ClientCAFile != "/client-ca.pem" || files.CAFile != "/server-ca.pem" {
		t.Fatalf("incorrect TLS file mapping: %+v", files)
	}
	for _, name := range []string{"ROSE_TLS_CERT", "ROSE_TLS_KEY", "ROSE_TLS_CLIENT_CA", "ROSE_TLS_CA"} {
		if _, exists := AsMap()[name]; !exists {
			t.Errorf("missing environment documentation for %s", name)
		}
	}
}
