package main

const minOperandStack = 32

// initialStack sizes a new operand stack from the argument count; it grows on
// demand up to StackSize (see growStack).
func initialStack(argCount int32) int32 {
	size := argCount + minOperandStack
	if size > StackSize && argCount < StackSize {
		size = StackSize
	}
	return size
}

func NewCoroutine(parent *Coroutine, fn *Function, stackSize int32) *Coroutine {
	if stackSize <= 0 {
		stackSize = minOperandStack
	}

	// The frame lives inside the coroutine so one allocation serves both.
	co := &Coroutine{
		Parent:       parent,
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

// growStack doubles co's operand stack (bounded by StackSize) and returns it.
//
//go:noinline
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

// release drops everything a finished coroutine references so it cannot keep
// the stack, cells or caller chain alive.
func (co *Coroutine) release() {
	co.OperandStack = nil
	co.Parent = nil
	co.Frame = CallFrame{}
}
