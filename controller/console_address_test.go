package controller

import (
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func configureConsoleAddressTest(t *testing.T) {
	t.Helper()
	server, console := system_setting.ServerAddress, system_setting.ConsoleAddress
	system_setting.ServerAddress = "https://api.example.com/"
	system_setting.ConsoleAddress = ""
	t.Cleanup(func() {
		system_setting.ServerAddress, system_setting.ConsoleAddress = server, console
	})
}

func TestConsoleAddressPasswordResetEmailUsesTrustedOrigin(t *testing.T) {
	for _, tc := range []struct{ name, console, expectedHost string }{
		{"separate console", "https://console.example.com", "console.example.com"},
		{"legacy server fallback", "", "api.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user, _ := setupSecurityEnrollmentTest(t)
			configureConsoleAddressTest(t)
			system_setting.ConsoleAddress = tc.console
			configureEmailDelivery(t, map[string]string{"EmailProvider": "smtp"})
			mailbox := newSecurityMailbox(t)
			email := "reset-console@example.com"
			require.NoError(t, model.DB.Model(user).Update("email", email).Error)
			t.Cleanup(func() { common.DeleteKey(email, common.PasswordResetPurpose) })

			request := httptest.NewRequest(http.MethodGet, "https://untrusted.example/api/reset_password?"+url.Values{"email": {email}}.Encode(), nil)
			request.Header.Set("Origin", "https://untrusted.example")
			request.Header.Set("Referer", "https://untrusted.example/forgot-password")
			request.Header.Set("X-Forwarded-Host", "untrusted.example")
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Request = request
			SendPasswordResetEmail(c)
			require.Equal(t, http.StatusOK, response.Code)
			assert.Contains(t, response.Body.String(), `"success":true`)
			mailbox.mutex.Lock()
			messages := append([]string(nil), mailbox.mail[email]...)
			mailbox.mutex.Unlock()
			require.Len(t, messages, 1)
			match := regexp.MustCompile(`href=['"]([^'"]+)['"]`).FindStringSubmatch(messages[0])
			require.Len(t, match, 2, "the delivered email must contain a reset link")
			resetURL, err := url.Parse(html.UnescapeString(match[1]))
			require.NoError(t, err)
			assert.Equal(t, "https", resetURL.Scheme)
			assert.Equal(t, tc.expectedHost, resetURL.Host, "request headers must not choose the reset-token destination")
			assert.Equal(t, "/user/reset", resetURL.Path)
			assert.Equal(t, email, resetURL.Query().Get("email"))
			token := resetURL.Query().Get("token")
			require.NotEmpty(t, token)
			assert.Len(t, resetURL.Query(), 2)

		})
	}
}

func TestConsoleAddressDiscoveryKeepsOAuthBackendEndpoints(t *testing.T) {
	configureConsoleAddressTest(t)
	previousEnabled := system_setting.GetOAuthServerSettings().Enabled
	system_setting.GetOAuthServerSettings().Enabled = true
	t.Cleanup(func() { system_setting.GetOAuthServerSettings().Enabled = previousEnabled })
	for _, tc := range []struct{ name, console, expectedBase string }{
		{"configured console", "https://console.example.com:8443", "https://console.example.com:8443"},
		{"legacy issuer fallback", "", "https://api.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			system_setting.ConsoleAddress = tc.console
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Request = httptest.NewRequest(http.MethodGet, "https://untrusted.example/.well-known/openid-configuration", nil)
			OAuthDiscovery(c)
			require.Equal(t, http.StatusOK, response.Code)
			var document map[string]any
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &document))
			assert.Equal(t, "https://api.example.com", document["issuer"])
			assert.Equal(t, tc.expectedBase+"/oauth2/auth", document["authorization_endpoint"])
			for field, path := range map[string]string{
				"token_endpoint": "/oauth2/token", "revocation_endpoint": "/oauth2/revoke",
				"userinfo_endpoint": "/oauth2/userinfo", "jwks_uri": "/oauth2/jwks",
			} {
				assert.Equal(t, "https://api.example.com"+path, document[field], field)
			}
		})
	}
}

func TestConsoleAddressPaymentReturnURLs(t *testing.T) {
	configureConsoleAddressTest(t)
	for _, tc := range []struct{ console, expectedBase string }{
		{"https://console.example.com", "https://console.example.com"},
		{"", "https://api.example.com"},
	} {
		t.Run(tc.expectedBase, func(t *testing.T) {
			system_setting.ConsoleAddress = tc.console
			assert.Equal(t, tc.expectedBase+"/wallet?pay=success", paymentReturnPath("/wallet?pay=success"))
			assert.Equal(t, tc.expectedBase+"/usage-logs", paymentReturnPath("/usage-logs"))
			assert.Equal(t, tc.expectedBase+"/wallet", service.PaymentReturnURL("/wallet"), "balance notices must point to the console wallet")
		})
	}
}

func TestConsoleAddressOptionValidationPersistenceAndStatus(t *testing.T) {
	_, identity := setupSecurityEnrollmentTest(t)
	configureConsoleAddressTest(t)
	configureEmailDelivery(t, map[string]string{"EmailProvider": "smtp"})
	for _, value := range []string{" https://console.example.com:8443/ ", "http://localhost:3000", "http://127.0.0.1:3000", "http://[::1]:3000", ""} {
		body, err := common.Marshal(OptionUpdateRequest{Key: "ConsoleAddress", Value: value})
		require.NoError(t, err)
		response := securityEnrollmentRequest(http.MethodPut, "/api/option/", string(body), "", identity, UpdateOption)
		require.Contains(t, response.Body.String(), `"success":true`)
		var stored model.Option
		require.NoError(t, model.DB.Where(&model.Option{Key: "ConsoleAddress"}).First(&stored).Error)
		assert.Equal(t, value, stored.Value, "configuration must survive persistence including clearing it")
		normalized := strings.TrimRight(strings.TrimSpace(value), "/")
		assert.Equal(t, normalized, system_setting.ConsoleAddress)
		assert.Equal(t, "https://api.example.com/", system_setting.ServerAddress)
		status := securityEnrollmentRequest(http.MethodGet, "/api/status", "", "", identity, GetStatus)
		var result struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, common.Unmarshal(status.Body.Bytes(), &result))
		assert.Equal(t, normalized, result.Data["console_address"])
		assert.Equal(t, "https://api.example.com/", result.Data["server_address"])
	}

	const trusted = "https://console.example.com"
	require.NoError(t, model.UpdateOption("ConsoleAddress", trusted))
	for _, invalid := range []string{
		"javascript:alert(1)", "//evil.example", "http://console.example.com", "https://user:password@console.example.com",
		"https://console.example.com/path", "https://console.example.com//", "https://console.example.com?next=evil", "https://console.example.com?",
		"https://console.example.com#fragment", "https://console.example.com#", "https://console.example.com\\evil", "https://console.example.com\r\nInjected: yes",
		"https://", "https://console.example.com:70000", "https://con sole.example.com",
	} {
		t.Run(invalid, func(t *testing.T) {
			body, err := common.Marshal(OptionUpdateRequest{Key: "ConsoleAddress", Value: invalid})
			require.NoError(t, err)
			response := securityEnrollmentRequest(http.MethodPut, "/api/option/", string(body), "", identity, UpdateOption)
			assert.Contains(t, response.Body.String(), `"success":false`)
			assert.Error(t, model.UpdateOption("ConsoleAddress", invalid), "direct writes must not bypass URL validation")
			assert.Error(t, model.UpdateOptionsBulk(map[string]string{"ConsoleAddress": invalid}), "bulk writes must not bypass URL validation")
			assert.Equal(t, trusted, system_setting.ConsoleAddress)
			var stored model.Option
			require.NoError(t, model.DB.Where(&model.Option{Key: "ConsoleAddress"}).First(&stored).Error)
			assert.Equal(t, trusted, stored.Value)
		})
	}
}
