log "sync:start";

var asyncFn = fn() async {
    log "async:body";
    return 1;
};

var promise = asyncFn();
log "sync:end";
promise->then(fn(value) {
    log "then:callback";
});

var awaitFn = fn() async {
    log "await:before";
    let value = await asyncFn();
    log "await:after";
};

awaitFn();
log "sync:after-await-call";
