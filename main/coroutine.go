package main

func NewCoroutine(parent *Coroutine, fn *Function, stackSize int32) *Coroutine {
	if stackSize <= 0 {
		stackSize = 64
	}

	var caller *CallFrame = nil
	if parent != nil {
		caller = parent.Frame
	}

	frame := &CallFrame{Caller: caller, Function: fn, Ip: 0, Env: make([]*Cell, fn.LocalCount)}
	cells := make([]Cell, fn.LocalCount)
	for i := range frame.Env {
		frame.Env[i] = &cells[i]
	}

	return &Coroutine{
		Parent:       parent,
		State:        CoroutineStateSuspended,
		Frame:        frame,
		Sp:           0,
		OperandStack: make([]OSValue, stackSize),
	}
}
