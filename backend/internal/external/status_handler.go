package external

import (
	"net/http"

	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
)

// StatusHandler 返回内存快照，认证由后台路由统一执行。
func (b *Bridge) StatusHandler(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, b.Status())
}
