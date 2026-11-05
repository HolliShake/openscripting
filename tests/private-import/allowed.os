import { println } from "core:io";
import { publicValue, __dunder__ } from "./module.os";

if (publicValue != 1 || __dunder__ != 3) {
	println("FAIL: public and dunder imports");
}

println("private import allowed cases ok");