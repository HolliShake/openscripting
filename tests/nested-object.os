import { println } from "core:io";

var world = {
	region: {
		station: {
			tracks: [
				{
					sensors: [
						{ reading: { value: 1 } },
						{ reading: { value: 2 } }
					]
				}
			]
		}
	}
};

if (world->region->station->tracks[0]->sensors[0]->reading->value != 1 || world["region"]["station"]["tracks"][0]["sensors"][1]["reading"]["value"] != 2) {
	println("FAIL: deeply nested object access");
}

world->region->station->tracks[0]->sensors[0]->reading = { value: 10 };
world["region"]["station"]["tracks"][0]["sensors"][1]["reading"]["value"] = 20;

if (world["region"]->station->tracks[0]["sensors"][0]->reading["value"] != 10 || world->region["station"]["tracks"][0]->sensors[1]->reading->value != 20) {
	println("FAIL: nested object reassignment or mixed access");
} else {
	println("nested object ok");
}
