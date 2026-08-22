package speech

import "fmt"

const (
	CodeSpeechDisabled        = "SPEECH_DISABLED"
	CodeInvalidRequest        = "INVALID_REQUEST"
	CodeUnauthorized          = "UNAUTHORIZED"
	CodeLimitExceeded         = "LIMIT_EXCEEDED"
	CodeEmptyAudio            = "EMPTY_AUDIO"
	CodeUpstreamTimeout       = "UPSTREAM_TIMEOUT"
	CodeUpstreamUnavailable   = "UPSTREAM_UNAVAILABLE"
	CodeUpstreamProtocolError = "UPSTREAM_PROTOCOL_ERROR"
	CodeClientCancelled       = "CLIENT_CANCELLED"
	CodeASRFinalFailed        = "ASR_FINAL_FAILED"
	CodeTextTooLong           = "TEXT_TOO_LONG"
	CodeTTSAlreadyRunning     = "TTS_ALREADY_RUNNING"
	CodeSpeechBusy            = "SPEECH_BUSY"
	CodeTTSUpstreamError      = "TTS_UPSTREAM_ERROR"
	CodeTTSUpstreamTimeout    = "TTS_UPSTREAM_TIMEOUT"
)

// Error is the provider-neutral speech error returned by services and
// adapters. Message is safe for clients; Cause is intended for internal logs.
type Error struct {
	Code      string
	Message   string
	Retryable bool
	Cause     error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Cause == nil {
		return fmt.Sprintf("speech: %s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("speech: %s: %s: %v", e.Code, e.Message, e.Cause)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Is makes errors.Is compare speech errors by stable code.
func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && e != nil && e.Code == other.Code
}

var (
	ErrSpeechDisabled  = &Error{Code: CodeSpeechDisabled, Message: "语音功能未启用"}
	ErrInvalidRequest  = &Error{Code: CodeInvalidRequest, Message: "语音请求参数不合法"}
	ErrUnauthorized    = &Error{Code: CodeUnauthorized, Message: "登录状态无效，请重新登录"}
	ErrLimitExceeded   = &Error{Code: CodeLimitExceeded, Message: "语音服务繁忙，请继续使用文字训练"}
	ErrEmptyAudio      = &Error{Code: CodeEmptyAudio, Message: "没有可识别的音频"}
	ErrUpstreamTimeout = &Error{
		Code: CodeUpstreamTimeout, Message: "语音服务响应超时，请继续使用文字训练", Retryable: true,
	}
	ErrUpstreamUnavailable = &Error{
		Code: CodeUpstreamUnavailable, Message: "语音服务暂时不可用，请继续使用文字训练", Retryable: true,
	}
	ErrUpstreamProtocol = &Error{
		Code: CodeUpstreamProtocolError, Message: "语音服务响应异常，请继续使用文字训练",
	}
	ErrClientCancelled = &Error{Code: CodeClientCancelled, Message: "语音请求已取消"}
	ErrASRFinalFailed  = &Error{
		Code: CodeASRFinalFailed, Message: "未能生成最终识别文本，请改用键盘输入", Retryable: true,
	}
	ErrTextTooLong       = &Error{Code: CodeTextTooLong, Message: "问题文字过长，已保留文字显示"}
	ErrTTSAlreadyRunning = &Error{
		Code: CodeTTSAlreadyRunning, Message: "当前问题正在生成语音，请稍候",
	}
	ErrSpeechBusy = &Error{
		Code: CodeSpeechBusy, Message: "语音服务繁忙，请继续使用文字训练",
	}
	ErrTTSUpstreamError = &Error{
		Code: CodeTTSUpstreamError, Message: "语音合成暂时不可用，请继续使用文字训练", Retryable: true,
	}
	ErrTTSUpstreamTimeout = &Error{
		Code: CodeTTSUpstreamTimeout, Message: "语音合成响应超时，请继续使用文字训练", Retryable: true,
	}
)

// WithCause returns a new error with the stable public fields of base and an
// internal cause. It never mutates the shared sentinel.
func WithCause(base *Error, cause error) *Error {
	if base == nil {
		return &Error{Code: CodeUpstreamUnavailable, Message: ErrUpstreamUnavailable.Message, Cause: cause}
	}
	return &Error{
		Code:      base.Code,
		Message:   base.Message,
		Retryable: base.Retryable,
		Cause:     cause,
	}
}
