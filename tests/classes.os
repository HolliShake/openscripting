import { println } from "core:io";

var Base::class {
    constructor: fn(value) {
        this->value = value;
    },
    getValue: fn() {
        return this->value;
    }
};

var Child::class(Base) {
    constructor: fn(value) {
        super(value + 1);
        this->child = true;
        return null;
    }
};

var Empty::class {
    ready: fn() {
        return true;
    }
};

var ExpressionClass = class {
    constructor: fn(value) {
        this->value = value;
    },
    getValue: fn() {
        return this->value;
    }
};

var child = Child(40);
var inheritedMethod = child->getValue;
var empty = Empty();
var expressionInstance = ExpressionClass(9);

if (child->getValue() != 41 || inheritedMethod() != 41 || child->child != true) {
    println("FAIL: inherited methods, bound receiver, or constructor return");
}
if (empty->ready() != true || expressionInstance->getValue() != 9) {
    println("FAIL: default constructor or class expression");
}

println("classes ok");
