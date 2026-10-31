package controller

import (
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func configureConsoleAddressTest(t *testing.T) {
	t.Helper()
	server, console, callback := system_setting.ServerAddress, system_setting.ConsoleAddress, operation_setting.CustomCallbackAddress
	system_setting.ServerAddress = "https://api.example.com/"
	system_setting.ConsoleAddress = ""
	operation_setting.CustomCallbackAddress = ""
	t.Cleanup(func() {
		system_setting.ServerAddress, system_setting.ConsoleAddress = server, console
		operation_setting.CustomCallbackAddress = callback
	})
}

func TestConsoleAddressPasswordResetEmailAndTokenLifecycle(t *testing.T) {
	for _, tc := range []struct{ name, console, expectedHost string }{
		{"separate console", " https://console.example.com/ ", "console.example.com"},
		{"legacy server fallback", "", "api.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user, identity := setupSecurityEnrollmentTest(t)
			configureConsoleAddressTest(t)
			system_setting.ConsoleAddress = tc.console
			configureEmailDelivery(t, map[string]string{"EmailProvider": "smtp"})
			mailbox := newSecurityMailbox(t)
			email := "reset+console@example.com"
			require.NoError(t, model.DB.Model(user).Update("email", email).Error)

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
			assert.Equal(t, email, resetURL.Query().Get("email"), "plus-addressed email must round-trip through the URL")
			token := resetURL.Query().Get("token")
			require.NotEmpty(t, token)
			assert.Len(t, resetURL.Query(), 2)

			for _, phase := range []string{"invalid", "expired", "valid", "replay"} {
				currentToken := token
				if phase == "invalid" {
					currentToken += "invalid"
				}
				body, err := common.Marshal(PasswordResetRequest{Token: currentToken})
				require.NoError(t, err)
				setExpiry := func(at time.Time) {
					require.NoError(t, model.DB.Model(&model.AuthFlow{}).Where("purpose = ?", model.AuthFlowPurposePasswordReset).Update("expires_at", at).Error)
				}
				if phase == "expired" {
					setExpiry(time.Now().Add(-time.Second))
				}
				resetResponse := securityEnrollmentRequest(http.MethodPost, "/api/user/reset", string(body), "", identity, ResetPassword)
				if phase == "expired" {
					setExpiry(time.Now().Add(time.Minute))
				}
				var result struct {
					Success bool   `json:"success"`
					Data    string `json:"data"`
				}
				require.NoError(t, common.Unmarshal(resetResponse.Body.Bytes(), &result))
				assert.Equal(t, phase == "valid", result.Success, phase)
				if phase == "valid" {
					stored, err := model.GetUserById(user.Id, true)
					require.NoError(t, err)
					assert.True(t, common.ValidatePasswordAndHash(result.Data, stored.Password), "successful reset changes the password")
					_, _, err = service.ValidateLoginSession(identity)
					assert.Error(t, err, "password reset invalidates the old dashboard session")
				} else {
					assert.Empty(t, result.Data, "failed reset must not disclose a new password")
				}
			}
		})
	}
}

// The registration code is stored in the database, so the instance that sent
// it need not be the one that registers the account.
func TestRegistrationEmailCodeIsUsedOnce(t *testing.T) {
	setupSecurityEnrollmentTest(t)
	configureEmailDelivery(t, map[string]string{"EmailProvider": "smtp"})
	mailbox := newSecurityMailbox(t)
	previous := []bool{common.RegisterEnabled, common.PasswordRegisterEnabled, common.EmailVerificationEnabled}
	common.RegisterEnabled, common.PasswordRegisterEnabled, common.EmailVerificationEnabled = true, true, true
	t.Cleanup(func() {
		common.RegisterEnabled, common.PasswordRegisterEnabled, common.EmailVerificationEnabled = previous[0], previous[1], previous[2]
	})
	email := "new-user@example.com"
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/verification?"+url.Values{"email": {email}}.Encode(), nil)
	SendEmailVerification(c)
	require.Contains(t, response.Body.String(), `"success":true`)
	mailbox.mutex.Lock()
	messages := append([]string(nil), mailbox.mail[email]...)
	mailbox.mutex.Unlock()
	require.Len(t, messages, 1)
	match := regexp.MustCompile(`<strong>([0-9a-f]{6})</strong>`).FindStringSubmatch(messages[0])
	require.Len(t, match, 2, "the email carries the code")

	register := func(username, code string) bool {
		body, err := common.Marshal(map[string]string{"username": username, "password": "Password123!", "email": email, "verification_code": code})
		require.NoError(t, err)
		response := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(response)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(string(body)))
		Register(c)
		return strings.Contains(response.Body.String(), `"success":true`)
	}
	assert.False(t, register("newuser1", "000000"), "a wrong code is rejected")
	assert.True(t, register("newuser1", match[1]))
	assert.False(t, register("newuser2", match[1]), "a code registers once")
}

func TestConsoleAddressDiscoveryKeepsOAuthBackendEndpoints(t *testing.T) {
	configureConsoleAddressTest(t)
	previousEnabled := system_setting.GetOAuthServerSettings().Enabled
	system_setting.GetOAuthServerSettings().Enabled = true
	t.Cleanup(func() { system_setting.GetOAuthServerSettings().Enabled = previousEnabled })
	for _, tc := range []struct{ name, console, expectedBase string }{
		{"configured console", " https://console.example.com:8443/ ", "https://console.example.com:8443"},
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

func TestConsoleAddressPaymentPagesPreserveCallbackOverrides(t *testing.T) {
	configureConsoleAddressTest(t)
	for _, tc := range []struct{ console, expectedBase string }{
		{"https://console.example.com/", "https://console.example.com"},
		{"", "https://api.example.com"},
	} {
		t.Run(tc.expectedBase, func(t *testing.T) {
			system_setting.ConsoleAddress = tc.console
			assert.Equal(t, tc.expectedBase+"/wallet?pay=success", paymentReturnPath("/wallet?pay=success"))
			assert.Equal(t, tc.expectedBase+"/usage-logs", paymentReturnPath("/usage-logs"))
			assert.Equal(t, tc.expectedBase+"/wallet", service.PaymentReturnURL("/wallet"), "balance notices must point to the console wallet")
			operation_setting.CustomCallbackAddress = ""
			assert.Equal(t, "https://api.example.com/", service.GetCallbackAddress())
			operation_setting.CustomCallbackAddress = "https://payments.example.com"
			assert.Equal(t, "https://payments.example.com", service.GetCallbackAddress())
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
		assert.Equal(t, value, system_setting.ConsoleAddress)
		assert.Equal(t, "https://api.example.com/", system_setting.ServerAddress)
		status := securityEnrollmentRequest(http.MethodGet, "/api/status", "", "", identity, GetStatus)
		var result struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, common.Unmarshal(status.Body.Bytes(), &result))
		assert.Equal(t, strings.TrimRight(strings.TrimSpace(value), "/"), result.Data["console_address"])
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

func TestConsoleAddressOptionRequiresRootAuthentication(t *testing.T) {
	user, _ := setupSecurityEnrollmentTest(t)
	configureConsoleAddressTest(t)
	configureEmailDelivery(t, map[string]string{"EmailProvider": "smtp"})
	require.NoError(t, model.UpdateOption("ConsoleAddress", "https://console.example.com"))
	session, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "console-address-test")
	require.NoError(t, err)
	router := gin.New()
	router.PUT("/api/option/", middleware.RootAuth(), UpdateOption)
	for _, tc := range []struct {
		name   string
		role   int
		status int
	}{
		{"anonymous", common.RoleCommonUser, http.StatusUnauthorized},
		{"ordinary user", common.RoleCommonUser, http.StatusForbidden},
		{"administrator", common.RoleAdminUser, http.StatusForbidden},
		{"root", common.RoleRootUser, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, model.DB.Model(user).Update("role", tc.role).Error)
			require.NoError(t, model.PublishUserAuthCache(user.Id))
			request := httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(`{"key":"ConsoleAddress","value":"https://new-console.example.com"}`))
			request.Header.Set("Content-Type", "application/json")
			if tc.name != "anonymous" {
				request.Header.Set("Authorization", "Bearer "+session.AccessToken)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			assert.Equal(t, tc.status, response.Code)
			expected := "https://console.example.com"
			if tc.role == common.RoleRootUser {
				expected = "https://new-console.example.com"
			}
			assert.Equal(t, expected, system_setting.ConsoleAddress)
			var stored model.Option
			require.NoError(t, model.DB.Where(&model.Option{Key: "ConsoleAddress"}).First(&stored).Error)
			assert.Equal(t, expected, stored.Value)
		})
	}
}
