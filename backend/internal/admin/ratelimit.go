package admin

import (
	"errors"
	"net/http"
	"reflect"

	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
)

// WithLimitSettings 注入限流规则管理服务。
func WithLimitSettings(settings *moderation.LimitSettings) Option {
	return func(s *Service) { s.limitSettings = settings }
}

// RateLimitRulesHandler 返回按注册表排序的规则和运行时乘数。
func (s *Service) RateLimitRulesHandler(w http.ResponseWriter, r *http.Request) {
	if s.limitSettings == nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInternal, "限流规则服务未配置"))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, s.rateLimitResponse())
}

func (s *Service) rateLimitResponse() any {
	snap := s.limitSettings.Current()
	rows := make([]map[string]any, 0)
	for _, meta := range moderation.RuleRegistry() {
		rule := snap.Rules[meta.Key]
		rows = append(rows, map[string]any{"key": meta.Key, "name": meta.Name, "group": meta.Group, "kind": meta.Kind, "algorithm": meta.Algorithm, "banSubject": meta.BanSubject, "limit": rule.Limit, "windowSeconds": rule.WindowSeconds, "banSeconds": rule.BanSeconds, "enabled": rule.Enabled, "minWindowSeconds": meta.MinWindowSeconds, "maxWindowSeconds": s.limitSettings.MaxWindowSeconds(meta.Key), "updatedAt": rule.UpdatedAt})
	}
	return map[string]any{"version": snap.Version, "multiplier": s.limitSettings.Multiplier(), "rules": rows}
}

// UpdateRateLimitRulesHandler 全量校验、保存配置并记录实际变化。
func (s *Service) UpdateRateLimitRulesHandler(w http.ResponseWriter, r *http.Request) {
	if s.limitSettings == nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInternal, "限流规则服务未配置"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req struct {
		Rules []moderation.Rule `json:"rules"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if len(req.Rules) != len(moderation.RuleRegistry()) {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "请提交全部限流规则"))
		return
	}
	s.rateUpdateMu.Lock()
	defer s.rateUpdateMu.Unlock()
	before := s.limitSettings.Current()
	adminID, _ := AdminIDFromContext(r.Context())
	if err := s.limitSettings.Update(r.Context(), req.Rules, adminID); err != nil {
		var invalid *moderation.ValidationError
		if errors.As(err, &invalid) {
			httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, err.Error()))
		} else {
			s.logger.Error("保存限流规则失败", "error", err)
			httpx.WriteError(w, httpx.NewError(httpx.CodeInternal, "保存限流规则失败"))
		}
		return
	}
	after := s.limitSettings.Current()
	changes := make(map[string]any)
	for key, rule := range after.Rules {
		old := before.Rules[key]
		// 更新时间不参与变更判断；仅审计管理员修改的业务值。
		a, b := old, rule
		a.UpdatedAt = b.UpdatedAt
		if !reflect.DeepEqual(a, b) {
			changes[string(key)] = map[string]any{"before": old, "after": rule}
		}
	}
	s.audit(r.Context(), adminID, "update_rate_limit_rules", "", changes)
	httpx.WriteJSON(w, http.StatusOK, s.rateLimitResponse())
}
