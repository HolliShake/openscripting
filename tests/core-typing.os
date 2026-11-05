import { instance } from "core:typing";
import { println } from "core:io";

var Base::class {
	baseMethod: fn() { return true; }
};
var Child::class(Base) {
	ready: fn() { return true; }
};
var Other::class {
	ready: fn() { return true; }
};

const child = Child();
if (!instance(child, Child) || !instance(child, Base)) {
	println("FAIL: instance class and superclass");
}
if (instance(child, Other) || instance(7, Base) || instance(null, Base)) {
	println("FAIL: instance non-matching values");
}
try {
	instance(child, 1);
	println("FAIL: instance accepted a non-class argument");
} catch (err) {
	if (err->message != "core:typing.instance expects a value and a class") {
		println("FAIL: instance invalid-class error", err->message);
	}
}

println("core typing ok");