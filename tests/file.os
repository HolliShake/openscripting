
var fact::fn(n) {
    log n;
    if (n <= 1) {
        return 1;
    }
    return n * fact(n - 1);
};


fact(5, 2);
