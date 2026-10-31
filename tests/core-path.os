import { println } from "core:io";
import { exists, isFile, isDir, isAbs, absolute, join, clean, base, dir, ext, relative } from "core:path";

const source = "tests/core-path.os";
const normalized = clean(source);
const fullPath = absolute(source);

if (normalized != clean("tests/./core-path.os") || base(source) != "core-path.os" || dir(source) != "tests" || ext(source) != ".os") {
    println("FAIL: path parts and clean");
}
if (isAbs(fullPath) == false || exists(fullPath) == false || isFile(fullPath) == false || isDir(".") == false) {
    println("FAIL: path checks and absolute");
}
if (join("tests", "nested", "..", "core-path.os") != normalized) {
    println("FAIL: join");
}
if (relative(fullPath, absolute(".")) != normalized) {
    println("FAIL: relative");
}

try {
    exists(123);
    println("FAIL: invalid path type was not raised");
} catch (err) {
    println("caught path error:", err);
}

println("core:path ok");
