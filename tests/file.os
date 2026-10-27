
var fact::fn(n) {
    if (n <= 1) {
        return 1;
    }
    return n * fact(n - 1);
};

log "result"; 

log fact(5);

var f::fn() {
    return 0;
};

try {
    log fact(5) / 2;
} catch(err) {
    log err;
}

var x = 2, z = x;

log x * 2 + z;

var ff = false, gg = ff;

log ff || true;

var arr = [1, 2, 3];

log arr;

var [a, b, c] = [1, 2, 3];

