import { println } from "core:io";

const closures = [null, null, null];
loop (let index = 0; index < 3; index++) {
	closures[index] = fn() {
		return index;
	};
}

if (closures[0]() != 0 || closures[1]() != 1 || closures[2]() != 2) {
	println("FAIL: for-loop closures did not capture each iteration");
}

const continued = [null, null, null];
loop (let index = 0; index < 3; index++) {
	continued[index] = fn() {
		return index;
	};
	continue;
}

if (continued[0]() != 0 || continued[1]() != 1 || continued[2]() != 2) {
	println("FAIL: continue skipped per-iteration capture");
}