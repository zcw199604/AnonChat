// Package persona 纯解析角色卡与人设包，不访问数据库或文件系统。
package persona

// Card 是解析后的角色卡，与角色卡数据列对应。
type Card struct {
	Name, Description, Personality, Scenario, FirstMessage, ExampleDialogue, SystemPrompt string
	HeartPrompt, ReasonPrompt, PostHistoryInstructions, CreatorNotes                      string
	AlternateGreetings                                                                    []string
	Lorebook                                                                              []LoreEntry
	Tools                                                                                 []string
	SourceFormat                                                                          string
	Extensions                                                                            map[string]any
}

// LoreEntry 是一条世界书条目。
type LoreEntry struct {
	Keys          []string `json:"keys"`
	Content       string   `json:"content"`
	Enabled       bool     `json:"enabled"`
	Constant      bool     `json:"constant"`
	CaseSensitive bool     `json:"case_sensitive"`
	Order         int      `json:"order"`
	Comment       string   `json:"comment"`
}

// ExpressionFile 是大小与类型已经校验的表情图片。
type ExpressionFile struct {
	Key, Description, FileName string
	Data                       []byte
}

// Result 是一次导入结果，警告供后台展示。
type Result struct {
	Card        Card
	Expressions []ExpressionFile
	Warnings    []string
}

// Option 配置解析时的媒体大小上限。
type Option func(*options)
type options struct{ maxImageBytes int64 }

// WithMaxImageBytes 使用媒体服务提供的单文件字节上限。
func WithMaxImageBytes(n int64) Option {
	return func(o *options) {
		if n > 0 {
			o.maxImageBytes = n
		}
	}
}
