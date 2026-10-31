import { println } from "core:io";

println("sync:start");

var immediate = fn() async {
    println("immediate:body");
    return "ready";
};

var pending = fn() async {
    println("pending:before-await");
    let value = await immediate();
    println("pending:after-await");
    return value;
};

var promise = pending();
promise->then(fn(value) {
    println("then:first");
});
promise->then(fn(value) {
    println("then:second");
});

var waiter = fn(label) async {
    println(label + ":before-await");
    let value = await promise;
    println(label + ":after-await");
};

waiter("waiter:one");
waiter("waiter:two");
println("sync:end");
