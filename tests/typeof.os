import { println } from "core:io";

var Example::class {
	constructor: fn(value) {
		this->value = value;
	},
	getValue: fn() {
		return this->value;
	}
};
var asyncValue = fn() async {
	return null;
};

if (typeof 1 != "number" || typeof 1n != "bigint" || typeof true != "boolean" ||
	typeof "text" != "string" || typeof null != "null") {
	println("FAIL: typeof primitive values");
}
if (typeof [] != "array" || typeof {} != "object" || typeof fn() {} != "function") {
	println("FAIL: typeof aggregate values or function");
}
if (typeof Example != "class" || typeof Example(7) != "object") {
	println("FAIL: typeof class or instance");
}
if (typeof println != "function" || typeof "text"->len != "function" ||
	typeof Example(7)->getValue != "function") {
	println("FAIL: typeof native or bound methods");
}
if (typeof asyncValue() != "promise") {
	println("FAIL: typeof promise");
}

println("typeof ok");