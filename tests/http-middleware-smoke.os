import { Express, cookieParser, fetch, json, raw, text, urlencoded } from "core:http";
import { println } from "core:io";

const app = Express();
app->use(json());
app->use(urlencoded({ extended: true }));
app->use(text());
app->use(raw());
app->use(cookieParser("smoke-secret"));
app->post("/json", fn(req, res) {
	res->send(req->body->name);
});
app->post("/form", fn(req, res) {
	res->send(req->body->user->name);
});
app->post("/text", fn(req, res) {
	if (req->is("text")) {
		res->send(req->body);
	} else {
		res->send("type mismatch");
	}
});
app->post("/raw", fn(req, res) {
	res->send(req->body);
});
app->get("/signed", fn(req, res) {
	res->cookie("token", "signed-value", { signed: true });
	res->send("issued");
});
app->get("/verify", fn(req, res) {
	res->send(req->signedCookies->token);
});

var address = app->listen("127.0.0.1:0");
var verify = fn() async {
	let jsonResponse = await fetch("http://" + address + "/json", {
		method: "POST",
		body: "{\"name\":\"Ada\"}",
		headers: { "Content-Type": "application/json" }
	});
	println("json parser:", await jsonResponse->text());
	let formResponse = await fetch("http://" + address + "/form", {
		method: "POST",
		body: "user[name]=Ada",
		headers: { "Content-Type": "application/x-www-form-urlencoded" }
	});
	println("urlencoded parser:", await formResponse->text());
	let textResponse = await fetch("http://" + address + "/text", {
		method: "POST",
		body: "plain",
		headers: { "Content-Type": "text/plain" }
	});
	println("text parser:", await textResponse->text());
	let rawResponse = await fetch("http://" + address + "/raw", {
		method: "POST",
		body: "AB",
		headers: { "Content-Type": "application/octet-stream" }
	});
	println("raw parser:", await rawResponse->text());
	let issued = await fetch("http://" + address + "/signed");
	let cookie = issued->headers["set-cookie"];
	let verified = await fetch("http://" + address + "/verify", {
		headers: { Cookie: cookie }
	});
	println("signed cookie:", await verified->text());
	app->close();
};
verify();