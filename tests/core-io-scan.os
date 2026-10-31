import { println, scan } from "core:io";

const first = scan("first: ");
const second = scan(42);

if (first != "alpha" || second != "beta") {
    println("FAIL: scan input behavior", first, second);
} else {
    println("core:io scan ok");
}
