import { println } from "core:io";
import { isEven:IsEven } from "lib:number";

import "./test.os";


var fact::fn(n) {
    if (n <= 1) {
        return 1;
    }
    return n * fact(n - 1);
};

println("result"); 

println(fact(5));

var f::fn() {
    return 0;
};

try {
    println(fact(5) / 2);
} catch(err) {
    println(err);
}

var x = 2, z = x;

println(x * 2 + z);

var ff = false, gg = ff;

println(ff || true);

var arr = [1, 2, 3];

println(arr);

const arrayValue = [1, 2, 3, ...[4,5,6], 7, 8, 9, ...[10, 11, 12, ...[13, 14, 15]]];

var [a, b, c] = arrayValue;

println(a);
println(b);
println(c);
println(arrayValue);

println([]);
println([13, 14]);
println([... [15, 16]]);


var obj = {
    "A": "65",
    "B": "66",
    ...{ C: 67}
};

println(obj);

var { A: Ey, C, B: B } = obj;

println(Ey);
println(B);
println(C);


var add::fn(a, b) {
    return a + b;
};

var varArg::fn(a...) {
    println(a);
};


varArg(1,2,3,4);

var varArgWithFixed::fn(prefix, values...) {
    println(prefix);
    println(values);
};

varArgWithFixed("fixed", 2, 3);
varArgWithFixed("fixed");
varArg();


var fun = fn() async {
    println("async body");
    return 40 + 2;
};

var pending = fun();
println("before await");

var nestedAsync = fn() async {
    return await fun();
};
nestedAsync();

var asyncChecks = fn() async {
    println(await pending);
    println(await pending);
};

asyncChecks();

println(">>" + obj->A);

var items = [
    "One",
    "Two",
    "Three"
];

println(items[1]);

println(obj["A"]);
println(obj["missing"]);
var objectKey = "B";
println(obj[objectKey]);

var asyncF::fn() async {
    return "Hello!";
};

asyncF()
    ->then(fn(data) {
        println("then " + data);
        return "nah!!";
      })
    ->then(fn(data) {
        println(data);
        return 1000;
    })
    ->then(fn(data) {
        println(data);
        return data + "asdasd";
    })
    ->catch(fn(data) {
        println(data);
    });


var ggll = (3 < 2) 
    ? "t" 
    : "f";

println(ggll);

var hg = 0;

loop (hg < 10) {
    println(hg);
    hg = hg + 1;
}

println(obj);
obj->A = 100;
println(obj);

println(items);
items[1] = 1000;
items[1] += 1000 + 2;
println(items);


var makeCounter::fn() {
    let c = 0;
    let _counter = fn() {
        c += 1;
        return c;
    };
    return [c, _counter];
};

var [count, inc] = makeCounter();

println(inc());
println(inc());
println(inc());

println(count++ + 2 - ++count + 1);
println(count);


var item = switch(100) {
    10, 20, 1002 => "Ok",
    default => "Bad"
};

println(">>>>");
println(item);



var Human::class {
    constructor: fn(age) {
        this->age = age;
    }
};

var Person::class(Human) {
    constructor: fn(name) {
        super(100);
        this->name = name;
    },

    getName: fn() {
        return this->name;
    }
};

var MyClass = class {
    constructor: fn(name) {
   
        this->name = name;
    },
};

println(MyClass("Doy"));


var person = Person("Doydoy");
println(person->getName());
println(person);

println([1,2,3]);


println(">>>>>>>");
println(IsEven(5));
println("hello world!!!", "DDD");


loop (let x = 0;x < 3;x++) {
    println(x);
}

/*
loop (;;) {
    println("Hola!");
}
*/

switch (4) {
    case 0:
    case 1, 2, 3, 4: {
        println("Inside");
        continue;
        println("No executed!");
    }
    println("Hello");
    break;
    default: {
        println("No val!");
    }
}

