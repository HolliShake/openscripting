import { println } from "core:io";

var started = 0;
var sequence::fn*() {
	started++;
	yield started;
	started++;
	yield started;
	return 9;
};

const iterator = sequence();
if (started != 0) {
	println("FAIL: generator ran before next()");
}

const first = iterator->next();
if (first->value != 1 || first->done != false || started != 1) {
	println("FAIL: first generator yield", first, started);
}

const second = iterator->next();
if (second->value != 2 || second->done != false || started != 2) {
	println("FAIL: second generator yield", second, started);
}

const completed = iterator->next();
if (completed->value != 9 || completed->done != true) {
	println("FAIL: generator return value", completed);
}

const afterCompletion = iterator->next();
if (afterCompletion->value != null || afterCompletion->done != true) {
	println("FAIL: completed generator next()", afterCompletion);
}

var collect::fn*(firstValue, rest...) {
	yield firstValue;
	yield rest->len();
	return rest[0];
};
const collected = collect("head", "tail", "last");
const collectedFirst = collected->next();
const collectedCount = collected->next();
const collectedReturn = collected->next();
if (collectedFirst->value != "head" || collectedFirst->done != false ||
	collectedCount->value != 2 || collectedCount->done != false ||
	collectedReturn->value != "tail" || collectedReturn->done != true) {
	println("FAIL: generator parameters and variadic arguments", collectedFirst, collectedCount, collectedReturn);
}

var makeCounter::fn(start) {
	let current = start;
	return fn*() {
		loop (current < start + 3) {
			yield current;
			current++;
		}
		return current;
	};
};
const counter = makeCounter(4)();
const counterFirst = counter->next();
const counterSecond = counter->next();
const counterThird = counter->next();
const counterDone = counter->next();
if (counterFirst->value != 4 || counterSecond->value != 5 || counterThird->value != 6 ||
	counterDone->value != 7 || counterDone->done != true) {
	println("FAIL: captured state and loop resumption", counterFirst, counterSecond, counterThird, counterDone);
}

var GeneratorBox::class {
	constructor: fn(value) {
		this->value = value;
	},
	values: fn*() {
		yield this->value;
		return this->value + 1;
	}
};
const boxIterator = GeneratorBox(20)->values();
const boxValue = boxIterator->next();
const boxReturn = boxIterator->next();
if (boxValue->value != 20 || boxValue->done != false || boxReturn->value != 21 || boxReturn->done != true) {
	println("FAIL: generator class method and this binding", boxValue, boxReturn);
}

var empty::fn*() {};
const emptyIterator = empty();
const emptyResult = emptyIterator->next();
if (emptyResult->value != null || emptyResult->done != true) {
	println("FAIL: empty generator completion", emptyResult);
}

var pause::fn*() {
	yield;
	return;
};
const pauseIterator = pause();
const pauseResult = pauseIterator->next();
const pauseDone = pauseIterator->next();
if (pauseResult->value != null || pauseResult->done != false ||
	pauseDone->value != null || pauseDone->done != true) {
	println("FAIL: valueless yield and return", pauseResult, pauseDone);
}

var loopSequence::fn*() {
	loop (let number = 1; number <= 5; number++) {
		yield number;
	}
	return 6;
};
var loopValues = [];
loop (let current = loopSequence(); current->hasNext();) {
	loopValues = [...loopValues, current->next()->value];
}
if (loopValues->len() != 5 || loopValues[0] != 1 || loopValues[1] != 2 ||
	loopValues[2] != 3 || loopValues[3] != 4 || loopValues[4] != 5) {
	println("FAIL: hasNext/value generator loop", loopValues);
}

println(loopValues);

println("generator smoke ok");
