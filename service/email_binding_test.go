package service

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateAccountEmailPreservesDomainPolicy(t *testing.T) {
	previousRestriction := common.EmailDomainRestrictionEnabled
	previousAliases := common.EmailAliasRestrictionEnabled
	previousWhitelist := common.EmailDomainWhitelist
	t.Cleanup(func() {
		common.EmailDomainRestrictionEnabled = previousRestriction
		common.EmailAliasRestrictionEnabled = previousAliases
		common.EmailDomainWhitelist = previousWhitelist
	})

	for _, testCase := range []struct {
		name        string
		email       string
		whitelist   []string
		disabled    bool
		blockAlias  bool
		expected    string
		expectedErr error
	}{
		{name: "exact domain", email: "user@edu.cn", whitelist: []string{"edu.cn"}, expected: "user@edu.cn"},
		{name: "child domain", email: "user@school.edu.cn", whitelist: []string{"edu.cn"}, expected: "user@school.edu.cn"},
		{name: "nested child domain", email: "user@mail.school.edu.cn", whitelist: []string{"edu.cn"}, expected: "user@mail.school.edu.cn"},
		{name: "normalized policy", email: " User@School.EDU.CN ", whitelist: []string{"", " EDU.CN "}, expected: "user@school.edu.cn"},
		{name: "unrelated domain", email: "user@edu.glnc.cn", whitelist: []string{"edu.cn"}, expectedErr: ErrAccountEmailRestricted},
		{name: "suffix without label boundary", email: "user@evil-edu.cn", whitelist: []string{"edu.cn"}, expectedErr: ErrAccountEmailRestricted},
		{name: "suffix before attacker domain", email: "user@edu.cn.example.com", whitelist: []string{"edu.cn"}, expectedErr: ErrAccountEmailRestricted},
		{name: "blank whitelist", email: "user@school.edu.cn", whitelist: []string{"", " "}, expectedErr: ErrAccountEmailRestricted},
		{name: "restriction disabled", email: "user@example.com", disabled: true, expected: "user@example.com"},
		{name: "plus alias blocked", email: "user+tag@school.edu.cn", whitelist: []string{"edu.cn"}, blockAlias: true, expectedErr: ErrAccountEmailRestricted},
		{name: "dot alias blocked", email: "first.last@school.edu.cn", whitelist: []string{"edu.cn"}, blockAlias: true, expectedErr: ErrAccountEmailRestricted},
		{name: "alias allowed", email: "user+tag@school.edu.cn", whitelist: []string{"edu.cn"}, expected: "user+tag@school.edu.cn"},
		{name: "length limit retained", email: strings.Repeat("a", 44) + "@edu.cn", whitelist: []string{"edu.cn"}, expectedErr: ErrAccountEmailInvalid},
		{name: "invalid address", email: "user@@edu.cn", whitelist: []string{"edu.cn"}, expectedErr: ErrAccountEmailInvalid},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			common.EmailDomainRestrictionEnabled = !testCase.disabled
			common.EmailAliasRestrictionEnabled = testCase.blockAlias
			common.EmailDomainWhitelist = testCase.whitelist
			email, err := ValidateAccountEmail(testCase.email)
			if testCase.expectedErr != nil {
				require.ErrorIs(t, err, testCase.expectedErr)
				assert.Empty(t, email)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, testCase.expected, email)
		})
	}
}
