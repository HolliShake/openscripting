package main

const minOperandStack = 32

const (
	CoroutineStateSuspended CoroutineState = iota
	CoroutineStateRunning
	CoroutineStateDead
)

type CoroutineState int32

type Coroutine struct {
	Parent       *Coroutine
	Caller       *Coroutine
	State        CoroutineState
	Frame        CallFrame
	Sp           int32
	OperandStack []OSValue
	Transfer     OSValue
	AsyncResult  *Promise
	AsyncCaller  *Coroutine
	StringRoot   string
	ResumeValue  OSValue
	ResumeReady  bool
	ResumeReject bool
}

// release drops everything a finished coroutine references so it cannot keep
// the stack, cells or caller chain alive.
func (co *Coroutine) Release() {
	co.OperandStack = nil
	co.Parent = nil
	co.Caller = nil
	co.AsyncResult = nil
	co.AsyncCaller = nil
	co.StringRoot = ""
	co.ResumeValue = OSValue{}
	co.ResumeReady = false
	co.ResumeReject = false
	co.Frame = CallFrame{}
}
func NewCoroutine(parent *Coroutine, fn *Function, stackSize int32) *Coroutine {
	if stackSize <= 0 {
		stackSize = minOperandStack
	}

	// The frame lives inside the coroutine so one allocation serves both.
	co := &Coroutine{
		Parent:       parent,
		Caller:       parent,
		State:        CoroutineStateSuspended,
		OperandStack: make([]OSValue, stackSize),
	}

	frame := &co.Frame
	if parent != nil {
		frame.Caller = &parent.Frame
	}
	frame.Function = fn
	frame.Env = make([]*Cell, fn.LocalCount)
	cells := make([]Cell, fn.LocalCount)
	for i := range frame.Env {
		frame.Env[i] = &cells[i]
	}

	return co
}

// InitialStack sizes a new operand stack from the argument count; it grows on
// demand up to StackSize (see growStack).
func InitialStack(argCount int32) int32 {
	size := argCount + minOperandStack
	if size > StackSize && argCount < StackSize {
		size = StackSize
	}
	return size
}

func SnapshotLexicalChain(parent *Coroutine) *Coroutine {
	if parent == nil {
		return nil
	}

	return &Coroutine{
		Parent: SnapshotLexicalChain(parent.Parent),
		Frame: CallFrame{
			Function: parent.Frame.Function,
			Env:      parent.Frame.Env,
		},
	}
}

// growStack doubles co's operand stack (bounded by StackSize) and returns it.
func growStack(co *Coroutine) []OSValue {
	current := int32(len(co.OperandStack))
	if current >= StackSize {
		panic("operand stack overflow")
	}
	grown := make([]OSValue, min(current*2, StackSize))
	copy(grown, co.OperandStack)
	co.OperandStack = grown
	return grown
}
