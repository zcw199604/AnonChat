package bot

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

// i16 返回 int16 指针（构造年龄用）。
func i16(v int16) *int16 { return &v }

// genTestUUID 生成一个测试用的随机 UUID。
func genTestUUID(t *testing.T) pgtype.UUID {
	t.Helper()
	id, err := genUUID()
	if err != nil {
		t.Fatalf("生成 UUID 失败: %v", err)
	}
	return id
}
