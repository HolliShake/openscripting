import { Express, fetch } from "core:http";
import { println } from "core:io";

const app = Express();
app->get("/hello", "hello");
const address = app->listen("127.0.0.1:0");

var check = fn() async {
	const response = await fetch("http://" + address + "/hello", { method: "OPTIONS" });
	const allow = response->headers->allow;
	println("OPTIONS status:", response->status);
	println("OPTIONS allow:", allow);
	if (response->status != 204 || allow != "GET, HEAD, OPTIONS") {
		println("FAIL: unexpected automatic OPTIONS response");
	} else {
		println("PASS: automatic OPTIONS");
	}
	app->close();
};

check();
