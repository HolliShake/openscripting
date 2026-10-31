import { println } from "core:io";

const huge = 1000000000000000000000000000000000n;
if (huge + 1n != 1000000000000000000000000000000001n) {
    println("FAIL: exact addition");
}
if (huge * 3n != 3000000000000000000000000000000000n) {
    println("FAIL: exact multiplication");
}
if (1000000000000000000000000000000001n / 3n != 333333333333333333333333333333333n) {
    println("FAIL: integer division");
}
if (10n % 3n != 1n || -(huge) != -1000000000000000000000000000000000n) {
    println("FAIL: remainder or unary minus");
}
if ((0xFFn & 0x0Fn) != 15n || (1n << 100n) != 1267650600228229401496703205376n) {
    println("FAIL: bitwise or shift");
}
if (+0b101n != 5n || ~0n != -1n) {
    println("FAIL: radix or unary operators");
}
var counter = 2n;
if (++counter != 3n || counter++ != 3n || counter != 4n || --counter != 3n || counter-- != 3n || counter != 2n) {
    println("FAIL: increment/decrement");
}
if (!(huge == huge) || huge != huge || huge < huge || !(huge <= huge) || huge > huge || !(huge >= huge)) {
    println("FAIL: comparison");
}
if (0n) {
    println("FAIL: zero truthiness");
}
if (1n) {
} else {
    println("FAIL: truthiness");
}

println("big number ok");
