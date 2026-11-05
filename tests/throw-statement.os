import { Error } from "core:error";
import { println } from "core:io";

try {
    throw "Some Error";
} catch (err) {
    if (err != "Some Error") println("FAIL: thrown string was changed", err);
}

try {
    throw 2;
} catch (err) {
    if (err != 2) println("FAIL: thrown number was changed", err);
}

try {
    throw null;
} catch (err) {
    if (err != null) println("FAIL: thrown null was changed", err);
}

const variable = "from variable";
try {
    throw variable;
} catch (err) {
    if (err != variable) println("FAIL: thrown variable was changed", err);
}

try {
    throw [1, 2, 3];
} catch (err) {
    if (err[0] != 1 || err[2] != 3) println("FAIL: thrown array was changed", err);
}

try {
    throw { reason: "object" };
} catch (err) {
    if (err->reason != "object") println("FAIL: thrown object was changed", err);
}

const problem = Error("Some error", 7);
try {
    throw problem;
} catch (err) {
    if (err->message != problem->message || err->line != problem->line) {
        println("FAIL: thrown Error was changed", err);
    }
}

var throwAsync::fn() async {
    throw ["async", 9];
};
throwAsync()->catch(fn(err) {
    if (err[0] != "async" || err[1] != 9) {
        println("FAIL: async thrown value was changed", err);
    }
});

println("throw statement ok");