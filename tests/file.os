
var fact::fn(n) {
    if (n <= 1) {
        return 1;
    }
    return n * fact(n - 1);
};

log "result"; log fact(5);

