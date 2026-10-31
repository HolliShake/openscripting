


var isEven::fn(n) {
    return n % 2 == 0;
};

var offset = 10;

var addOffset::fn(n) {
    return n + offset;
};

var calls = 0;

var nextCall::fn() {
    calls++;
    return calls;
};