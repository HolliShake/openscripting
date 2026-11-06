package main

const minOperandStack = 32

const (
	CoroutineStateSuspended CoroutineState = iota
	CoroutineStateRunning
	CoroutineStateDead
)

type CoroutineState int

type Coroutine struct {
	// Parent is the lexical environment chain used to resolve captured outer variables.
	Parent *Coroutine
	// Caller is the dynamic caller resumed after a normal synchronous function returns.
	Caller *Coroutine
	// State prevents dead or already-running coroutines from being scheduled again.
	State CoroutineState
	// Frame holds this invocation's function, instruction pointer, environment, and frame link.
	Frame CallFrame
	// Sp is the next free operand-stack slot and is saved whenever execution suspends.
	Sp int
	// OperandStack stores temporary expression values for this coroutine's bytecode.
	OperandStack []OSValue
	// AsyncResult is resolved/rejected when this coroutine completes an async function.
	AsyncResult *Promise
	// AsyncCaller resumes the waiting coroutine after an awaited async child settles.
	AsyncCaller *Coroutine
	// StringRoot keeps backing bytes alive when a resumed coroutine receives a string value.
	StringRoot string
	// ResumeValue is the value delivered when an awaited Promise settles.
	ResumeValue OSValue
	// ResumeReady distinguishes a queued resume from an ordinary coroutine entry.
	ResumeReady bool
	// ResumeReject tells the entry path to raise ResumeValue instead of pushing it as a result.
	ResumeReject bool
	// SyncCall marks a caller waiting for a native collection method's synchronous script callback.
	SyncCall bool
	// SyncRoot marks the callback's root frame so nested helper returns do not finish the callback early.
	SyncRoot bool
	// SyncDone signals that the root callback returned and SyncResult contains its return value.
	SyncDone bool
	// SyncResult carries the callback return value back across the native method boundary.
	SyncResult OSValue
	// SyncBoundary carries an uncaught callback error back to the collection method without a process panic.
	SyncBoundary *Promise
	// snapshot caches this coroutine's immutable lexical snapshot (see
	// SnapshotLexicalChain). Function and Env never change during a
	// coroutine's life, so the snapshot can be built once and shared.
	snapshot *Coroutine
}

type coroutineStorage struct {
	Coroutine
	stack [minOperandStack]OSValue
}

// Release drops everything a finished coroutine references so it cannot keep
// the stack, cells or caller chain alive. It is idempotent: raise() may
// release the same coroutine twice.
func (co *Coroutine) Release() {
	clear(co.OperandStack)
	co.OperandStack = nil
	co.Parent = nil
	co.Caller = nil
	co.AsyncResult = nil
	co.AsyncCaller = nil
	co.StringRoot = ""
	co.ResumeValue = OSValue{}
	co.ResumeReady = false
	co.ResumeReject = false
	co.SyncCall = false
	co.SyncRoot = false
	co.SyncDone = false
	co.SyncResult = OSValue{}
	co.SyncBoundary = nil
	co.snapshot = nil
	co.Frame = CallFrame{}
}

// Pointer table and cells are allocated together, so a call costs one
// allocation for its environment instead of two. A captured *Cell points into
// the block and keeps only the block alive, exactly like the old cells slice.
type envBlock4 struct {
	ptrs  [4]*Cell
	cells [4]Cell
}

type envBlock8 struct {
	ptrs  [8]*Cell
	cells [8]Cell
}

type envBlock16 struct {
	ptrs  [16]*Cell
	cells [16]Cell
}

func newEnv(n int) []*Cell {
	switch {
	case n <= 0:
		return nil
	case n <= 4:
		b := new(envBlock4)
		for i := 0; i < n; i++ {
			b.ptrs[i] = &b.cells[i]
		}
		return b.ptrs[:n:n]
	case n <= 8:
		b := new(envBlock8)
		for i := 0; i < n; i++ {
			b.ptrs[i] = &b.cells[i]
		}
		return b.ptrs[:n:n]
	case n <= 16:
		b := new(envBlock16)
		for i := 0; i < n; i++ {
			b.ptrs[i] = &b.cells[i]
		}
		return b.ptrs[:n:n]
	}
	env := make([]*Cell, n)
	cells := make([]Cell, n)
	for i := range env {
		env[i] = &cells[i]
	}
	return env
}

func NewCoroutine(parent *Coroutine, fn *Function, stackSize int) *Coroutine {
	if stackSize <= 0 {
		stackSize = minOperandStack
	}

	// The frame lives inside the coroutine so one allocation serves both.
	var co *Coroutine
	if stackSize <= minOperandStack {
		storage := &coroutineStorage{
			Coroutine: Coroutine{
				Parent: parent,
				Caller: parent,
				State:  CoroutineStateSuspended,
			},
		}
		co = &storage.Coroutine
		co.OperandStack = storage.stack[:stackSize]
	} else {
		co = &Coroutine{
			Parent:       parent,
			Caller:       parent,
			State:        CoroutineStateSuspended,
			OperandStack: make([]OSValue, stackSize),
		}
	}

	frame := &co.Frame
	if parent != nil {
		frame.Caller = &parent.Frame
	}
	frame.Function = fn
	frame.Env = newEnv(fn.LocalCount)

	return co
}

// InitialStack sizes a new operand stack from the argument count; it grows on
// demand up to StackSize (see growStack).
func InitialStack(argCount int) int {
	size := argCount + minOperandStack
	if size > StackSize && argCount < StackSize {
		size = StackSize
	}
	return size
}

// SnapshotLexicalChain returns an immutable copy of parent's lexical chain.
// Each coroutine's snapshot is built once and cached, so repeated async calls
// from the same frame (and from frames sharing ancestors) reuse the chain
// instead of copying it every time.
func SnapshotLexicalChain(parent *Coroutine) *Coroutine {
	if parent == nil {
		return nil
	}
	if parent.snapshot != nil {
		return parent.snapshot
	}

	snap := &Coroutine{
		Parent: SnapshotLexicalChain(parent.Parent),
		Frame: CallFrame{
			Function: parent.Frame.Function,
			Env:      parent.Frame.Env,
		},
	}
	parent.snapshot = snap
	return snap
}

// growStack doubles co's operand stack (bounded by StackSize) and returns it.
func growStack(co *Coroutine) []OSValue {
	current := len(co.OperandStack)
	if current >= StackSize {
		panic("operand stack overflow")
	}
	grown := make([]OSValue, min(current*2, StackSize))
	copy(grown, co.OperandStack)
	co.OperandStack = grown
	return grown
}
