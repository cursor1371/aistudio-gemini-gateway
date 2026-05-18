package common

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// GenerateOpaqueID 生成一个带前缀的随机 ID。
// randomBytes 表示随机字节数；若 <= 0，则使用 8 字节。
func GenerateOpaqueID(prefix string, randomBytes int) string {
	prefix = strings.TrimSpace(prefix)
	if randomBytes <= 0 {
		randomBytes = 8
	}
	buf := make([]byte, randomBytes)
	if _, err := rand.Read(buf); err != nil {
		// 极端情况下退化为时间戳兜底，避免返回空值。
		return fmt.Sprintf("%s%x", prefix, time.Now().UnixNano())
	}
	return prefix + hex.EncodeToString(buf)
}

// GenerateConnectionID 生成 WebSocket 连接 ID。
func GenerateConnectionID() string {
	return GenerateOpaqueID("conn_", 8)
}

// GenerateMessageID 生成 WS 消息 ID。
func GenerateMessageID() string {
	return GenerateOpaqueID("msg_", 8)
}

// GenerateProviderID 生成默认 Provider ID。
func GenerateProviderID() string {
	return GenerateOpaqueID("aistudio-", 8)
}
