import { Error } from "core:error";
import { println } from "core:io";

const created = Error("example", 42);
if (created->message != "example" || created->line != 42) {
    println("FAIL: Error constructor");
}

var divisor = 0;
try {
    1 / divisor;
} catch (err) {
    if (err->line != 12 || err->message != "division by zero") {
        println("FAIL: caught Error fields", err->line, err->message);
    }
}

println("core error ok");

var fail::fn(value) async {
    return 1 / value;
};
fail(0)->catch(fn(err) {
    if (err->message != "division by zero" || err->line != 21) {
        println("FAIL: async Error fields", err->line, err->message);
    }
});

