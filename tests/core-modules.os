import { println } from "core:io";
import { readText, writeText, appendText, exists, remove } from "core:file";
import { PI, E, abs, ceil, floor, round, sqrt, sin, cos, tan, logarithm, exp, pow, min, max } from "core:math";

const path = "core-modules.tmp";

if (writeText(path, "hello") == false) {
    println("FAIL: writeText");
}
if (appendText(path, " world") == false) {
    println("FAIL: appendText");
}
if (exists(path) == false || readText(path) != "hello world") {
    println("FAIL: readText/exists");
}
if (abs(-4) != 4 || ceil(1.2) != 2 || floor(1.8) != 1 || round(1.6) != 2) {
    println("FAIL: rounding functions");
}
if (sqrt(81) != 9 || pow(2, 5) != 32 || min(4, 2, 8) != 2 || max(4, 2, 8) != 8) {
    println("FAIL: numeric functions");
}
if (sin(0) != 0 || cos(0) != 1 || tan(0) != 0 || logarithm(E) < 0.99 || exp(1) < 2.71 || PI < 3) {
    println("FAIL: math constants/functions");
}
if (remove(path) == false || exists(path)) {
    println("FAIL: remove");
}

try {
    readText(path);
    println("FAIL: missing-file error was not raised");
} catch (err) {
    println("caught file error:", err);
}

try {
    sqrt(-1);
    println("FAIL: math-domain error was not raised");
} catch (err) {
    println("caught math error:", err);
}

try {
    abs("not a number");
    println("FAIL: math type error was not raised");
} catch (err) {
    println("caught math type error:", err);
}

println("core modules ok");
