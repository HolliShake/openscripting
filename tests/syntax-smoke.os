import { println } from "core:io";
import { isEven: importedIsEven, addOffset } from "./import.os";

// Lexical forms, Unicode identifiers, literals, and operators.
const café = "café";
var score = 1_000e-3 + 2;
score += 2;
score -= 1;
score *= 2;
score /= 2;
score %= 7;
const bitwise = ((5 << 2) >> 1) | (7 & 3) ^ 1;
const largeInteger = 12345678901234567890n + 10n;
const escaped = "line\n\t\"quoted\"";
const ternary = score > 0 && café->contains("é") ? "ready" : "missing";

// Declarations, destructuring, spreads, property access, and callbacks.
var globalValue = null;
const sourceArray = [1, 2, 3];
const [firstValue, secondValue, thirdValue] = sourceArray;
const sourceObject = { label: "syntax", active: true };
const { label: itemLabel, active } = sourceObject;
const combinedArray = [0, ...sourceArray, 4];
const combinedObject = { ...sourceObject, count: combinedArray->len() };
combinedArray[0] = 9;
combinedObject["extra"] = "index assignment";
const mappedValues = sourceArray->map(fn(value, index) {
	return value * 2 + index;
});
const filteredValues = sourceArray->filter(fn(value) {
	return value > 1;
});
const foundValue = sourceArray->find(fn(value) {
	return value == 2;
});

// Named functions, variadic parameters, closures, and class forms.
var smokeAdd::fn(left, right) {
	return left + right;
};
var smokeCollect::fn(first, rest...) {
	return [first, ...rest];
};
var makeAdder = fn(amount) {
	return fn(value) {
		return value + amount;
	};
};
const addFive = makeAdder(5);

var SmokeBase::class {
	constructor: fn(value) {
		this->value = value;
	},
	read: fn() {
		return this->value;
	}
};
var SmokeChild::class(SmokeBase) {
	constructor: fn(value) {
		super(value + 1);
		this->child = true;
	}
};
const instance = SmokeChild(8);
const ExpressionClass = class {
	getValue: fn() {
		return 12;
	}
};
var LoopWorker::class {
	constructor: fn(value) {
		this->value = value;
	},
	getValue: fn() {
		return this->value;
	},
	calculate: fn(amount) async {
		const increment = await fn() async {
			return amount;
		}();
		return this->value + increment;
	}
};

const workers = [null, null, null];
const workerTasks = [null, null, null];
loop (let workerIndex = 0; workerIndex < 3; workerIndex++) {
	workers[workerIndex] = LoopWorker(workerIndex * 10);
	workerTasks[workerIndex] = fn() async {
		return await workers[workerIndex]->calculate(2);
	};
}

// Conditional, loop, switch statement/expression, and exception syntax.
var loopTotal = 0;
loop (let index = 0; index < 4; index++) {
	if (index == 1) {
		continue;
	} else {
		loopTotal += index;
	}
}
loop (loopTotal < 5) {
	loopTotal++;
}
loop (;;) {
	break;
}

var switchLabel = switch (loopTotal) {
	5 => "five",
	6, 7 => "nearby",
	default => "other"
};
var switchTotal = 0;
switch (2) {
	case 1:
		switchTotal = -1;
		break;
	case 2, 3:
		switchTotal = 2;
		break;
	default:
		switchTotal = -2;
}
try {
	const zero = 0;
	const invalid = 1 / zero;
	println(invalid);
} catch (error) {
	globalValue = error->message;
}

// Async functions, await, and promise chaining.
var smokeAsync::fn(value) async {
	const resolved = await fn() async {
		return value + 1;
	}();
	return resolved * 2;
};
var checkAsyncWorkers::fn() async {
	const firstResult = await workerTasks[0]();
	const secondResult = await workerTasks[1]();
	const thirdResult = await workerTasks[2]();
	if (firstResult != 2 || secondResult != 12 || thirdResult != 22 || workers[0]->getValue() != 0 || workers[1]->getValue() != 10 || workers[2]->getValue() != 20) {
		println("FAIL: loop class allocation, captured async calls, await, or methods", firstResult, secondResult, thirdResult);
	} else {
		println("async loop/class smoke ok");
	}
};
checkAsyncWorkers()->catch(fn(error) {
	println("FAIL: async loop/class rejection", error);
});

if (café != "café" || escaped != "line\n\t\"quoted\"" || score != 4 || bitwise != 10 || largeInteger != 12345678901234567900n) {
	println("FAIL: literals, Unicode, or operators");
}
if (firstValue != 1 || secondValue != 2 || thirdValue != 3 || combinedArray[0] != 9 || combinedArray[3] != 3) {
	println("FAIL: arrays, destructuring, spread, or indexing");
}
if (itemLabel != "syntax" || active != true || combinedObject->count != 5 || combinedObject->extra != "index assignment") {
	println("FAIL: objects, destructuring, spread, or properties");
}
if (mappedValues[0] != 2 || mappedValues[2] != 8 || filteredValues->len() != 2 || foundValue != 2) {
	println("FAIL: array callback methods");
}
if (smokeAdd(2, 3) != 5 || smokeCollect("a", "b", "c")->len() != 3 || addFive(7) != 12) {
	println("FAIL: functions, variadic parameters, or closures");
}
if (instance->read() != 9 || instance->child != true || ExpressionClass()->getValue() != 12) {
	println("FAIL: classes, inheritance, or class expressions");
}
if (loopTotal != 5 || switchLabel != "five" || switchTotal != 2 || globalValue != "division by zero") {
	println("FAIL: control flow, switch, or try/catch");
}
if (!importedIsEven(8) || importedIsEven(7) || addOffset(2) != 12) {
	println("FAIL: relative named imports");
}

println("syntax smoke ok");