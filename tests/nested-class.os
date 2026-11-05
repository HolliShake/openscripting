import { println } from "core:io";

var Sensor::class {
	constructor: fn(value) {
		this->reading = value;
	}
};

var Rack::class {
	constructor: fn(sensors) {
		this->sensors = sensors;
	}
};

var Station::class {
	constructor: fn(racks) {
		this->racks = racks;
	}
};

var Region::class {
	constructor: fn(stations) {
		this->stations = stations;
	}
};

var world = Region([
	Station([
		Rack([
			Sensor(11),
			Sensor(22)
		]),
		Rack([
			Sensor(33)
		])
	])
]);

if (world->stations[0]->racks[0]->sensors[0]->reading != 11 || world["stations"][0]["racks"][0]["sensors"][1]["reading"] != 22 || world->stations[0]->racks[1]->sensors[0]->reading != 33) {
	println("FAIL: nested class member access or indexing");
}

world->stations[0]->racks[0]->sensors[0]->reading = 110;
world["stations"][0]["racks"][0]["sensors"][1]["reading"] = 220;

if (world["stations"][0]->racks[0]["sensors"][0]->reading != 110 || world->stations[0]["racks"][0]->sensors[1]["reading"] != 220) {
	println("FAIL: nested class member reassignment");
} else {
	println("nested class ok");
}
