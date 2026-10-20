package risk

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// Attempt phases tell the resolver where the routed group comes from.
const (
	PhaseHTTPFirst = "http_first"
	PhaseHTTPLoop  = "http_loop"
	PhaseHTTPRetry = "http_retry"
	PhaseWSSelect  = "ws_select"
	PhaseWSLocked  = "ws_locked"
)

// GroupResolveInput carries the authorized routing result of one attempt.
type GroupResolveInput struct {
	Phase       string
	SelectGroup string
	LockedGroup string
	ChannelID   int
	Model       string
}

// ResolveAttemptGroup returns the concrete group an attempt is audited under.
// Only authorized routing state is read; the request body is never trusted.
// An auto request whose group cannot be determined yields an empty group.
func ResolveAttemptGroup(c *gin.Context, in GroupResolveInput) (group string, source string, viaAuto bool) {
	usingGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	_, pinned, _ := service.GetChannelConstraints(c).ResolvedPin()
	if usingGroup != "auto" {
		switch {
		case in.Phase == PhaseWSLocked:
			return usingGroup, GroupSourceWSLocked, false
		case in.Phase == PhaseWSSelect:
			return usingGroup, GroupSourceWSSelect, false
		case pinned:
			return usingGroup, GroupSourcePinned, false
		case in.Phase == PhaseHTTPRetry:
			return usingGroup, GroupSourceRetrySelect, false
		}
		return usingGroup, GroupSourceUsingGroup, false
	}

	userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	candidates := service.GetRequestAutoGroups(c, userGroup)
	allowed := func(g string) bool {
		return g != "" && g != "auto" && slices.Contains(candidates, g)
	}
	switch in.Phase {
	case PhaseHTTPRetry, PhaseWSSelect:
		if allowed(in.SelectGroup) {
			if in.Phase == PhaseWSSelect {
				return in.SelectGroup, GroupSourceWSSelect, true
			}
			return in.SelectGroup, GroupSourceRetrySelect, true
		}
	case PhaseHTTPFirst, PhaseHTTPLoop:
		autoGroup := common.GetContextKeyString(c, constant.ContextKeyAutoGroup)
		if allowed(autoGroup) && model.IsChannelEnabledForGroupModel(autoGroup, in.Model, in.ChannelID) {
			return autoGroup, GroupSourceAutoGroup, true
		}
	case PhaseWSLocked:
		if allowed(in.LockedGroup) && model.IsChannelEnabledForGroupModel(in.LockedGroup, in.Model, in.ChannelID) {
			return in.LockedGroup, GroupSourceWSLocked, true
		}
	}
	for _, g := range candidates {
		if g != "auto" && model.IsChannelEnabledForGroupModel(g, in.Model, in.ChannelID) {
			return g, GroupSourcePinnedCandidate, true
		}
	}
	return "", GroupSourceAutoUnresolved, true
}

// ExemptionScope is the group directory view needed to decide exemption.
type ExemptionScope struct {
	GroupNamespaces map[string]string
	Exempt          map[string]bool
	Bypass          map[string]bool
}

// IsBypassed reports whether an attempt stays out of the audit entirely. Only
// a concrete token group can be bypassed; auto requests are always audited.
func IsBypassed(scope ExemptionScope, group string, viaAuto bool) bool {
	if viaAuto || group == "" {
		return false
	}
	namespace, ok := scope.GroupNamespaces[group]
	return ok && scope.Bypass[namespace]
}

// IsExempt reports whether an attempt skips the audit. Auto requests are never
// exempt, even when they land on an exempt group.
func IsExempt(scope ExemptionScope, group string, viaAuto bool) bool {
	if viaAuto || group == "" {
		return false
	}
	namespace, ok := scope.GroupNamespaces[group]
	return ok && scope.Exempt[namespace]
}

// NamespaceFor maps a resolved group to its isolation namespace. Groups not
// yet reconciled keep their own provisional namespace so they never share
// records with another group.
func (s ExemptionScope) NamespaceFor(group string) string {
	if group == "" {
		return ReservedAutoNamespace
	}
	if namespace, ok := s.GroupNamespaces[group]; ok {
		return namespace
	}
	sum := sha256.Sum256([]byte(group))
	return "g:" + hex.EncodeToString(sum[:12])
}
