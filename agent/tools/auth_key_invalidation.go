package tools

import "sync"

var telegramAuthKeyInvalidatorMu sync.RWMutex
var telegramAuthKeyInvalidator func()

func SetAuthKeyInvalidator(fn func()) {
	telegramAuthKeyInvalidatorMu.Lock()
	defer telegramAuthKeyInvalidatorMu.Unlock()
	telegramAuthKeyInvalidator = fn
}

func notifyTelegramAgentAuthKeyChanged() {
	telegramAuthKeyInvalidatorMu.RLock()
	fn := telegramAuthKeyInvalidator
	telegramAuthKeyInvalidatorMu.RUnlock()
	if fn != nil {
		fn()
	}
}
