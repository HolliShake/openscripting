import { println } from "core:io";
import { exists } from "core:path";

try {
    exists(123);
    println("FAIL: invalid core function argument was not raised");
} catch (err) {
    if (err->message != "core:path.exists expects path strings") {
        println("FAIL: core function error was not catchable", err->message);
    }
}

try {
    [123]->map(exists);
    println("FAIL: core function error in collection callback was not raised");
} catch (err) {
    if (err->message != "core:path.exists expects path strings") {
        println("FAIL: collection callback error was not catchable", err->message);
    }
}

var makeValue::fn() async {
    return 123;
};
makeValue()->then(exists)->catch(fn(err) {
    if (err->message != "core:path.exists expects path strings") {
        println("FAIL: Promise native callback error was not catchable", err->message);
    }
});

println("core native errors ok");