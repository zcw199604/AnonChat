package identity

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
)

// adjectives 是昵称的形容词词表（不少于 30 个）。
var adjectives = []string{
	"迷路", "安静", "好奇", "温柔", "暴躁", "忧郁", "开心", "害羞",
	"勇敢", "慵懒", "机智", "淘气", "孤独", "神秘", "认真", "佛系",
	"话痨", "社恐", "早睡", "熬夜", "爱吃", "爱笑", "沉默", "飘忽",
	"梦幻", "清醒", "倔强", "随性", "敏锐", "缓慢", "快乐", "古怪",
}

// animals 是昵称的动物词表（不少于 30 个）。
var animals = []string{
	"水獭", "猫咪", "柴犬", "兔子", "狐狸", "刺猬", "海豚", "企鹅",
	"考拉", "浣熊", "松鼠", "长颈鹿", "斑马", "树懒", "羊驼", "熊猫",
	"仓鼠", "鹦鹉", "蝴蝶", "鲸鱼", "水母", "海豹", "麋鹿", "渡鸦",
	"猞猁", "貂熊", "蜜獾", "旱獭", "锦鲤", "蜗牛", "蜻蜓", "萤火虫",
}

// GenerateNickname 生成形如“迷路的水獭#3721”的随机昵称。
func GenerateNickname() string {
	adj := adjectives[randIntN(len(adjectives))]
	animal := animals[randIntN(len(animals))]
	num := 1000 + randIntN(9000) // 1000..9999，始终 4 位
	return fmt.Sprintf("%s的%s#%d", adj, animal, num)
}

// GeneratePlainNickname 生成形如“迷路的水獭”的随机昵称：不带“#”与编号。
// 供会拒绝含过多数字昵称的外站（chat2019、晚风）使用。
func GeneratePlainNickname() string {
	adj := adjectives[randIntN(len(adjectives))]
	animal := animals[randIntN(len(animals))]
	return fmt.Sprintf("%s的%s", adj, animal)
}

// GenerateAvatarSeed 生成 16 字节随机数的十六进制字符串（32 字符）。
func GenerateAvatarSeed() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败极罕见，退化为固定前缀以便调用方仍能工作。
		panic("生成 avatarSeed 失败: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// randIntN 返回 [0, n) 的密码学安全随机整数。
func randIntN(n int) int {
	if n <= 0 {
		return 0
	}
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0
	}
	return int(v.Int64())
}
