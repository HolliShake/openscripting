var fail::fn(divisor) async {
    return 1 / divisor;
};

fail(0)
    ->catch(fn(err) {
        log "caught " + err;
        return "recovered";
    })
    ->then(fn(value) {
        log value;
    });
