import { println } from "core:io";

var add::fn(left, right) {
	return left + right;
};
var subtract::fn(left, right) {
	return left - right;
};
var multiply::fn(left, right) {
	return left * right;
};

var index = 0;
var result = 0;
loop (index < 40) {
	result = add(index, 2);
	result = subtract(result, 1);
	result = multiply(result, 1);
	index++;
}

println(result);
println(add("a", "b"));