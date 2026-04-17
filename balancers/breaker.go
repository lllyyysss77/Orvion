package balancers

import (
	"sync"
	"time"
)

type State int

const (
	StateClosed   State = iota // 正常运行
	StateOpen                  // 熔断触发
	StateHalfOpen              // 探测恢复
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

type Node struct {
	state        State     // 熔断状态
	failCount    int       // 失败次数
	successCount int       // 成功次数
	expiry       time.Time // 冷却结束时间
}

// BreakerOpenEvent 熔断进入 Open 状态时的事件。
type BreakerOpenEvent struct {
	Key       uint
	PrevState State
	Reason    string
	FailCount int
	OpenUntil time.Time
	At        time.Time
}

func (n *Node) Reset(state State) {
	n.state = state
	n.failCount = 0
	n.successCount = 0
}

var (
	mu          sync.Mutex
	nodes       = make(map[uint]*Node)
	MaxFailures = 5                // 最多失败次数
	SleepWindow = 60 * time.Second // 冷却时间
	MaxRequests = 2                // 在 HalfOpen 状态下, 如果请求成功次数超过此数值，熔断器关闭（恢复）；如果有一个失败，重新进入 Open 状态
)

var (
	breakerHookMu     sync.RWMutex
	breakerOpenHookFn func(BreakerOpenEvent)
)

// SetBreakerOpenHook 设置熔断进入 Open 状态时的回调。
func SetBreakerOpenHook(hook func(BreakerOpenEvent)) {
	breakerHookMu.Lock()
	breakerOpenHookFn = hook
	breakerHookMu.Unlock()
}

func emitBreakerOpen(event BreakerOpenEvent) {
	breakerHookMu.RLock()
	hook := breakerOpenHookFn
	breakerHookMu.RUnlock()
	if hook == nil {
		return
	}
	go hook(event)
}

type Breaker struct {
	Balancer
}

func BalancerWrapperBreaker(balancer Balancer) *Breaker {
	mu.Lock()
	defer mu.Unlock()
	for key, node := range nodes {
		if node.state == StateOpen && node.expiry.Before(time.Now()) {
			node.Reset(StateHalfOpen)
		}
		if node.state == StateOpen {
			balancer.Delete(key)
		}
	}
	return &Breaker{Balancer: balancer}
}

func (b *Breaker) Pop() (uint, error) {
	key, err := b.Balancer.Pop()
	if err != nil {
		return 0, err
	}
	mu.Lock()
	defer mu.Unlock()
	if _, ok := nodes[key]; !ok {
		nodes[key] = &Node{state: StateClosed}
	}
	return key, nil
}

func (b *Breaker) Delete(key uint) {
	b.failCountAdd(key)
	b.Balancer.Delete(key)
}

func (b *Breaker) Reduce(key uint) {
	b.failCountAdd(key)
	b.Balancer.Reduce(key)
}

func (b *Breaker) failCountAdd(key uint) {
	mu.Lock()
	var openEvent *BreakerOpenEvent
	if node, ok := nodes[key]; ok {
		node.failCount += 1
		if node.state == StateClosed && node.failCount >= MaxFailures {
			failCount := node.failCount
			prevState := node.state
			node.Reset(StateOpen)
			node.expiry = time.Now().Add(SleepWindow)
			openEvent = &BreakerOpenEvent{
				Key:       key,
				PrevState: prevState,
				Reason:    "max_failures_reached",
				FailCount: failCount,
				OpenUntil: node.expiry,
				At:        time.Now(),
			}
		}

		if node.state == StateHalfOpen {
			failCount := node.failCount
			prevState := node.state
			node.Reset(StateOpen)
			node.expiry = time.Now().Add(SleepWindow)
			openEvent = &BreakerOpenEvent{
				Key:       key,
				PrevState: prevState,
				Reason:    "half_open_probe_failed",
				FailCount: failCount,
				OpenUntil: node.expiry,
				At:        time.Now(),
			}
		}
	}
	mu.Unlock()
	if openEvent != nil {
		emitBreakerOpen(*openEvent)
	}
}

func (b *Breaker) Success(key uint) {
	mu.Lock()
	defer mu.Unlock()
	if node, ok := nodes[key]; ok {
		if node.state == StateHalfOpen {
			node.successCount += 1
			if node.successCount >= MaxRequests {
				node.Reset(StateClosed)
			}
		}
	}
	b.Balancer.Success(key)
}
