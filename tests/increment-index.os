import { println } from "core:io";

var items = [10, 20];
println(items[1]++ + 2);
println(items[1]);
println(++items[0]);
println(items[0]);

var evaluations = 0;
var getItems::fn() {
	evaluations++;
	return items;
};
var getIndex::fn() {
	evaluations++;
	return 0;
};
println(getItems()[getIndex()]++);
println(evaluations);
println(items[0]);

evaluations = 0;
println(++getItems()[getIndex()]);
println(evaluations);
println(items[0]);

evaluations = 0;
println(getItems()[getIndex()]--);
println(evaluations);
println(items[0]);

evaluations = 0;
println(--getItems()[getIndex()]);
println(evaluations);
println(items[0]);
