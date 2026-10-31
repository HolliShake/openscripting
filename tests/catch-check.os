import { println } from "core:io";

var fail::fn(divisor) async {
    return 1 / divisor;
};

fail(0)
    ->catch(fn(err) {
        println("caught " + err);
        return "recovered";
    })
    ->then(fn(value) {
        println(value);
    });
