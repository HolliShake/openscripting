
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
    "A": "65",
    "B": "66",
    ...{ C: 67}
};

log obj;

var { A: Ey, C, B: B } = obj;

log Ey;
log B;
log C;


var add::fn(a, b) {
    return a + b;
};

var varArg::fn(a...) {
    log a;
};


varArg(1,2,3,4);

var varArgWithFixed::fn(prefix, values...) {
    log prefix;
    log values;
};

varArgWithFixed("fixed", 2, 3);
varArgWithFixed("fixed");
varArg();


var fun = fn() async {
    log "async body";
    return 40 + 2;
};

var pending = fun();
log "before await";

var nestedAsync = fn() async {
    return await fun();
};
nestedAsync();

var asyncChecks = fn() async {
    log await pending;
    log await pending;
};

asyncChecks();

log ">>" + obj->A;

var items = [
    "One",
    "Two",
    "Three"
];

log items[1];

log obj["A"];
log obj["missing"];
var objectKey = "B";
log obj[objectKey];

var asyncF::fn() async {
    return "Hello!";
};

asyncF()
    ->then(fn(data) {
        log "then " + data;
        return "nah!!";
      })
    ->then(fn(data) {
        log data;
        return 1000;
    })
    ->then(fn(data) {
        log data;
        return data + "asdasd";
    })
    ->catch(fn(data) {
        log data;
    });


var ggll = (3 < 2) 
    ? "t" 
    : "f";

log ggll;

var hg = 0;

loop (hg < 10) {
    log hg;
    hg = hg + 1;
}

log obj;
obj->A = 100;
log obj;

log items;
items[1] = 1000;
items[1] += 1000 + 2;
log items;


var makeCounter::fn() {
    let c = 0;
    let _counter = fn() {
        c += 1;
        return c;
    };
    return [c, _counter];
};

var [count, inc] = makeCounter();

log inc();
log inc();
log inc();

log count++ + 2 - ++count + 1;
log count;


var item = switch(100) {
    10, 20, 1002 => "Ok",
    default => "Bad"
};

log ">>>>";
log item;



var Person::class {
    constructor: fn(name) {
        this->name = name;
    },

    getName: fn() {

    }
};