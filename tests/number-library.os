import { isEven, isOdd, isPositive, isNegative, isZero, isInteger } from "lib:number";
import { println } from "core:io";

if (!isEven(4) || !isEven(-6) || isEven(3) || isEven(2.5)) {
    println("FAIL: isEven");
}
if (!isOdd(3)) {
    println("FAIL: isOdd positive");
}
if (!isOdd(-5)) {
    println("FAIL: isOdd negative");
}
if (isOdd(4)) {
    println("FAIL: isOdd even");
}
if (isOdd(2.5)) {
    println("FAIL: isOdd fractional");
}
if (!isPositive(2) || isPositive(0) || isPositive(-1)) {
    println("FAIL: isPositive");
}
if (!isNegative(-2) || isNegative(0) || isNegative(1)) {
    println("FAIL: isNegative");
}
if (!isZero(0) || isZero(0.1) || isZero(-1)) {
    println("FAIL: isZero");
}
if (!isInteger(3) || !isInteger(-2) || isInteger(2.5)) {
    println("FAIL: isInteger");
}

println("number library ok");