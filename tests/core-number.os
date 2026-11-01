import { parseInt, parseFloat, isFinite, isNaN, sign, clamp, MAX_VALUE, MIN_VALUE, NaN, Infinity } from "core:number";
import { println } from "core:io";

if (parseInt("42") != 42 || parseInt("  -17tail") != -17) {
	println("FAIL: decimal parseInt");
}
if (parseInt("0x2a") != 42 || parseInt("101tail", 2) != 5 || parseInt("z", 36) != 35) {
	println("FAIL: radix parseInt");
}
if (parseInt("tail") != null) {
	println("FAIL: invalid parseInt");
}
if (parseFloat("  -3.125e2tail") != -312.5 || parseFloat(".5rest") != 0.5 || parseFloat("2e+tail") != 2) {
	println("FAIL: parseFloat prefix");
}
if (parseFloat("not a number") != null || parseFloat("Infinity") != Infinity) {
	println("FAIL: invalid parseFloat or Infinity");
}
if (!isFinite(1) || isFinite(Infinity) || !isNaN(NaN) || isNaN(1)) {
	println("FAIL: numeric predicates");
}
if (sign(-9) != -1 || sign(0) != 0 || sign(7) != 1 || clamp(12, 0, 10) != 10 || clamp(-2, 0, 10) != 0) {
	println("FAIL: sign or clamp");
}
if (MAX_VALUE <= 1 || MIN_VALUE <= 0) {
	println("FAIL: numeric constants");
}

println("core number ok");