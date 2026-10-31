

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

log MyClass("Doy");


var person = Person("Doydoy");
log person->getName();
log person;

log [1,2,3];
