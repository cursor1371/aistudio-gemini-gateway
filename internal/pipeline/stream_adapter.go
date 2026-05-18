package pipeline

import "bytes"

// adaptedStreamChunk 表示一个已完成协议适配的流式片段。
// DownstreamPayload 是"纯 Gemini JSON 事件 payload"，
// 不携带 data:/event: 包装——SSE 帧封装由 HTTP handler 层负责。
type adaptedStreamChunk struct {
	DownstreamPayload []byte
}

// geminiStreamAdapter 负责把 wsrelay 层回来的 stream chunk
// 统一适配为网关内部稳定的 Gemini-only 下游契约。
//
// 工作模式：
//   - SSE 模式：把上游 SSE 文本解帧为纯 JSON payload 后下发。
//   - raw 模式：直接透传，不做事件解帧。
type geminiStreamAdapter struct {
	useSSE  bool
	decoder *sseEventDecoder
}

func newGeminiStreamAdapter(useSSE bool) *geminiStreamAdapter {
	adapter := &geminiStreamAdapter{
		useSSE: useSSE,
	}
	if useSSE {
		adapter.decoder = &sseEventDecoder{}
	}
	return adapter
}

// Adapt 把单个上游 payload 适配成 0..N 个稳定的下游 chunk。
func (a *geminiStreamAdapter) Adapt(payload []byte) []adaptedStreamChunk {
	if len(payload) == 0 {
		return nil
	}

	// raw 非 SSE 模式：直接透传。
	if !a.useSSE {
		return []adaptedStreamChunk{
			{DownstreamPayload: cloneBytes(payload)},
		}
	}

	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return nil
	}

	// SSE 模式下兼容两类输入：
	// 1. provider 回传完整/分片 SSE 文本
	// 2. provider 直接回传"纯 JSON payload"
	//
	// 为避免形成 data: data: {...} 的双层包装，
	// 统一把 SSE 文本解帧成"纯 JSON payload"。
	if (a.decoder != nil && a.decoder.HasBuffered()) || looksLikeSSEPayload(trimmed) {
		frames := a.decoder.Feed(payload)
		return a.wrapFrames(frames)
	}

	// provider 直接回传原始 JSON，视为一个逻辑事件。
	return a.wrapFrames([][]byte{trimmed})
}

// Flush 在上游 stream 结束时尝试把缓冲中的半截 SSE 事件冲刷出来。
// 兼容以下场景：
//   - 最后一帧没有以空行结束
//   - event/data 被拆成多个 WS chunk，刚好在结束时拼齐
func (a *geminiStreamAdapter) Flush() []adaptedStreamChunk {
	if !a.useSSE || a.decoder == nil {
		return nil
	}
	return a.wrapFrames(a.decoder.Flush())
}

func (a *geminiStreamAdapter) wrapFrames(frames [][]byte) []adaptedStreamChunk {
	if len(frames) == 0 {
		return nil
	}

	out := make([]adaptedStreamChunk, 0, len(frames))
	for _, frame := range frames {
		trimmed := bytes.TrimSpace(frame)
		if len(trimmed) == 0 {
			continue
		}
		out = append(out, adaptedStreamChunk{
			DownstreamPayload: cloneBytes(trimmed),
		})
	}
	return out
}

// =========================
// SSE 文本解帧器
// =========================

// sseEventDecoder 负责把流式到达的 SSE 文本拆分为独立事件的 data payload。
// 它内部维护一个字节缓冲区，按 "\n\n" 事件边界进行切分。
type sseEventDecoder struct {
	buf []byte
}

// HasBuffered 判断缓冲区中是否有尚未完成的 SSE 帧数据。
func (d *sseEventDecoder) HasBuffered() bool {
	return d != nil && len(bytes.TrimSpace(d.buf)) > 0
}

// Feed 向解帧器注入一段新数据，返回已经完整解帧的 JSON payload 列表。
func (d *sseEventDecoder) Feed(chunk []byte) [][]byte {
	if d == nil || len(chunk) == 0 {
		return nil
	}

	d.buf = append(d.buf, chunk...)
	d.buf = normalizeSSENewlines(d.buf)

	var out [][]byte
	for {
		idx := bytes.Index(d.buf, []byte("\n\n"))
		if idx < 0 {
			break
		}

		frame := cloneBytes(d.buf[:idx])
		d.buf = cloneBytes(d.buf[idx+2:])

		if payload, ok := parseSSEFrame(frame); ok {
			out = append(out, payload)
		}
	}
	return out
}

// Flush 尝试把剩余缓冲内容作为最后一个 SSE 事件输出。
func (d *sseEventDecoder) Flush() [][]byte {
	if d == nil {
		return nil
	}

	trimmed := bytes.TrimSpace(d.buf)
	d.buf = nil
	if len(trimmed) == 0 {
		return nil
	}

	if payload, ok := parseSSEFrame(trimmed); ok {
		return [][]byte{payload}
	}

	// 容错：剩余内容若本身就是合法 JSON 片段，也允许直接吐出。
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		return [][]byte{cloneBytes(trimmed)}
	}

	return nil
}

// parseSSEFrame 从单个 SSE 帧文本中提取 data payload。
// 支持：
//   - 标准 "data: {...}" 格式
//   - 多行 data 拼接
//   - 纯 JSON 容错
func parseSSEFrame(frame []byte) ([]byte, bool) {
	trimmed := bytes.TrimSpace(frame)
	if len(trimmed) == 0 {
		return nil, false
	}

	// 非 SSE 的纯 JSON 容错。
	if !looksLikeSSEPayload(trimmed) {
		return cloneBytes(trimmed), true
	}

	lines := bytes.Split(trimmed, []byte("\n"))
	dataLines := make([][]byte, 0, len(lines))

	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		switch {
		case bytes.HasPrefix(line, []byte(":")):
			// SSE 注释行，忽略。
			continue

		case bytes.HasPrefix(line, []byte("event:")):
			// event 名称行，Gemini 下游只需要 data payload，可忽略。
			continue

		case bytes.HasPrefix(line, []byte("data:")):
			part := bytes.TrimSpace(line[len("data:"):])
			if len(part) == 0 {
				continue
			}
			if bytes.Equal(part, []byte("[DONE]")) {
				return nil, false
			}
			dataLines = append(dataLines, cloneBytes(part))

		default:
			// 容错：单行 JSON 文本也视为一个有效逻辑事件。
			if len(dataLines) == 0 && (line[0] == '{' || line[0] == '[') {
				return cloneBytes(line), true
			}
		}
	}

	if len(dataLines) == 0 {
		return nil, false
	}
	return bytes.Join(dataLines, []byte("\n")), true
}

// looksLikeSSEPayload 判断一段 payload 是否看起来像 SSE 帧文本。
func looksLikeSSEPayload(payload []byte) bool {
	if len(payload) == 0 {
		return false
	}
	return bytes.HasPrefix(payload, []byte("data:")) ||
		bytes.HasPrefix(payload, []byte("event:")) ||
		bytes.HasPrefix(payload, []byte(":"))
}

// normalizeSSENewlines 统一行尾为 \n，兼容 \r\n 和 \r 风格。
func normalizeSSENewlines(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := bytes.ReplaceAll(in, []byte("\r\n"), []byte("\n"))
	out = bytes.ReplaceAll(out, []byte("\r"), []byte("\n"))
	return out
}
