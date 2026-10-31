import { println } from "core:io";

println("sync:start");

var asyncFn = fn() async {
    println("async:body");
    return 1;
};

var promise = asyncFn();
println("sync:end");
promise->then(fn(value) {
    println("then:callback");
});

var awaitFn = fn() async {
    println("await:before");
    let value = await asyncFn();
    println("await:after");
};

awaitFn();
println("sync:after-await-call");
