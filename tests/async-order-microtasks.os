log "sync:start";

var immediate = fn() async {
    log "immediate:body";
    return "ready";
};

var pending = fn() async {
    log "pending:before-await";
    let value = await immediate();
    log "pending:after-await";
    return value;
};

var promise = pending();
promise->then(fn(value) {
    log "then:first";
});
promise->then(fn(value) {
    log "then:second";
});

var waiter = fn(label) async {
    log label + ":before-await";
    let value = await promise;
    log label + ":after-await";
};

waiter("waiter:one");
waiter("waiter:two");
log "sync:end";
