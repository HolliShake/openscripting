import { Express, fetch } from "core:http";
import { println } from "core:io";

const app = Express();
var handled = 0;
app->get("/ready", fn(req, res) {
	res->send("ready");
});
app->get("/count", fn(req, res) {
	handled = handled + 1;
	res->send(handled);
});
app->post("/echo", fn(req, res) {
	res->send(req->rawBody);
});
const address = app->listen("127.0.0.1:0");

var runLoad = fn() async {
	let ready = await fetch("http://" + address + "/ready");
	if (ready->status != 200 || await ready->text() != "ready") {
		throw "HTTP load test readiness failed";
	}
	let index = 0;
	loop (index < 200) {
		let response = await fetch("http://" + address + "/count");
		let body = await response->json();
		if (response->status != 200 || body != index + 1) {
			throw "HTTP sequential load response mismatch";
		}
		index = index + 1;
	}
	let echoed = await fetch("http://" + address + "/echo", {
		method: "POST",
		body: "payload-through-load-path",
		headers: { "Content-Type": "text/plain" }
	});
	if (echoed->status != 200 || await echoed->text() != "payload-through-load-path") {
		throw "HTTP load echo failed";
	}
	if (handled != 200) {
		throw "HTTP load handler count mismatch";
	}
	app->close();
	println("PASS: HTTP sequential load (200 requests)");
};
runLoad();