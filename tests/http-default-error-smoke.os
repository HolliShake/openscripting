import { Express, fetch } from "core:http";
import { println } from "core:io";

const app = Express();
app->get("/custom", fn(req, res) {
	throw { status: 418, message: "custom failure" };
});
app->get("/production", fn(req, res) {
	throw { statusCode: 503, message: "private detail" };
});
var address = app->listen("127.0.0.1:0");

var checkErrors = fn() async {
	let custom = await fetch("http://" + address + "/custom");
	let customBody = await custom->text();
	println("custom error:", custom->status, customBody);
	app->set("env", "production");
	let production = await fetch("http://" + address + "/production");
	let productionBody = await production->text();
	println("production error:", production->status, productionBody);
	if (custom->status == 418 && customBody == "custom failure" && production->status == 503 && productionBody == "Service Unavailable") {
		println("PASS: default error response semantics");
	} else {
		println("FAIL: default error response semantics");
	}
	app->close();
};

checkErrors();
