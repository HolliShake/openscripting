import { println } from "core:io";

var fail::fn(value) {
	return 1 / value;
};
var asyncValue::fn(value) async {
	return value * 2;
};
var addFactor::fn(value) {
	return value + 3;
};
var makeScaled::fn(source, scale) {
	return source->map(fn(value) {
		return value * scale;
	});
};
var nestedAsync::fn(value) async {
	return value * 4;
};
var callNestedAsync::fn(value) {
	return nestedAsync(value);
};

const values = [1, 2, 3, 4];
const factor = 3;
const doubled = values->map(fn(value, index, source) {
	if (source[0] != 1) {
		return 0;
	}
	return value * factor + index;
});
const throughHelper = values->map(fn(value) {
	return addFactor(value);
});
const scaled = makeScaled(values, 5);
const evens = values->filter(fn(value) {
	return value % 2 == 0;
});
const found = values->find(fn(value, index) {
	return value > 2 && index == 2;
});

if (doubled[0] != 3 || doubled[3] != 15 || throughHelper[0] != 4 || throughHelper[3] != 7 || scaled[0] != 5 || scaled[3] != 20 || evens[0] != 2 || evens[1] != 4 || found != 3) {
	println("FAIL: array map/filter/find");
}
if (values[0] != 1 || values[3] != 4 || values->find(fn(value) { return value > 10; }) != null) {
	println("FAIL: array method source or missing result");
}
try {
	values->map(fn(value) {
		return fail(value - 2);
	});
	println("FAIL: callback error was not propagated");
} catch (err) {
	if (err->message != "division by zero") {
		println("FAIL: callback error message", err->message);
	}
}
const pendingValues = values->map(asyncValue);
if (pendingValues[0]->then == null || pendingValues[3]->then == null) {
	println("FAIL: async callback results are not promises");
}
const nestedPending = values->map(callNestedAsync);
if (nestedPending[0]->then == null || nestedPending[3]->then == null) {
	println("FAIL: nested async callback results are not promises");
}

const record = { alpha: 2, beta: 4, gamma: 6 };
const mappedRecord = record->map(fn(value, key, source) {
	if (source->beta != 4) {
		return 0;
	}
	if (key == "alpha") {
		return value + 10;
	}
	return value + 20;
});
const filteredRecord = record->filter(fn(value, key) {
	return key != "beta" && value > 2;
});
const foundRecord = record->find(fn(value, key) {
	return key == "beta" && value == 4;
});
if (mappedRecord->alpha != 12 || mappedRecord->gamma != 26 || filteredRecord->alpha != null || filteredRecord->beta != null || filteredRecord->gamma != 6 || foundRecord != 4) {
	println("FAIL: object map/filter/find");
}
const overrideRecord = { map: 7, value: 9 };
if (overrideRecord->map != 7) {
	println("FAIL: object own method-name property precedence");
}

println("array methods ok");