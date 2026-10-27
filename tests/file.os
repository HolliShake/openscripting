
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

const arrayValue = [1, 2, 3, ...[4,5,6], 7, 8, 9, ...[10, 11, 12, ...[13, 14, 15]]];

var [a, b, c] = arrayValue;

log a;
log b;
log c;
log arrayValue;

log [];
log [13, 14];
log [...[15, 16]];


var obj = {
     A : "65",
    "b": "66"
};

log obj;

