import { Express, cookieParser, fetch, json, urlencoded } from "core:http";
import { println } from "core:io";

const app = Express();
const child = Express({ mergeParams: true });
app->set("env", "production");
app->set("x-powered-by", false);
app->set("etag", "strong");
app->set("405", true);
app->set("body limit", 128);
app->use(json({ limit: 32 }));
app->use(urlencoded({ extended: true }));
app->use(cookieParser("production-secret"));

app->get("/health", fn(req, res) {
	res->json({ ok: true, mode: app->get("env") });
});
app->get("/private-error", fn(req, res) {
	throw { status: 500, message: "private production detail" };
});
app->get("/validator", fn(req, res) {
	res->send("stable production payload");
});
app->post("/json", fn(req, res) {
	res->send(req->body->name);
});
app->post("/form", fn(req, res) {
	res->send(req->body->account->name);
});
app->post("/unsupported-parser", fn(req, res) {
	res->send(req->is("json"));
});
app->get("/signed", fn(req, res) {
	res->cookie("session", "production-session", { signed: true });
	res->send("issued");
});
app->get("/signed-check", fn(req, res) {
	res->send(req->signedCookies->session);
});
app->get("/callback-control", fn(req, res) {
	let values = [1]->map(fn(value) { return value + 1; });
	res->send(values[0]);
});
app->get("/format", fn(req, res) {
	res->format({ html: fn() { "selected"; } });
	res->send("format response");
});
app->get("/stream", fn(req, res) {
	res->write("first:");
	res->flush();
	res->end("last");
});
app->get("/file", fn(req, res) {
	res->sendFile("http-file-fixture.txt", { root: "../tests" });
});
child->get("/member/:member", fn(req, res) {
	res->send(req->params->team + ":" + req->params->member);
});
child->get("/escape", fn(req, res, next) {
	next("router");
});
app->use("/teams/:team", child);
app->get("/teams/:team/escape", fn(req, res) {
	res->send("parent:" + req->params->team);
});
child->get("/added-after-mount", "live-router-route");

const address = app->listen("127.0.0.1:0");
var failures = 0;
var check = fn(label, condition) {
	if (condition) {
		println("PASS:", label);
	} else {
		failures = failures + 1;
		println("FAIL:", label);
	}
};

var run = fn() async {
	let health = await fetch("http://" + address + "/health");
	let healthBody = await health->json();
	check("production health response", health->status == 200 && healthBody->ok && healthBody->mode == "production");
	check("powered-by disabled", health->headers["x-powered-by"] == null);

	let hiddenError = await fetch("http://" + address + "/private-error");
	let hiddenBody = await hiddenError->text();
	check("production error redaction", hiddenError->status == 500 && hiddenBody == "Internal Server Error");

	let jsonResponse = await fetch("http://" + address + "/json", {
		method: "POST", body: "{\"name\":\"Ada\"}", headers: { "Content-Type": "application/json" }
	});
	check("JSON request parsing", jsonResponse->status == 200 && await jsonResponse->text() == "Ada");
	let malformed = await fetch("http://" + address + "/json", {
		method: "POST", body: "{bad", headers: { "Content-Type": "application/json" }
	});
	check("malformed JSON maps to 400", malformed->status == 400);
	let parserLimit = await fetch("http://" + address + "/json", {
		method: "POST", body: "{\"name\":\"a body larger than thirty two bytes\"}", headers: { "Content-Type": "application/json" }
	});
	check("parser size limit maps to 413", parserLimit->status == 413);
	let requestLimit = await fetch("http://" + address + "/json", {
		method: "POST", body: "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", headers: { "Content-Type": "application/json" }
	});
	check("request body limit maps to 413", requestLimit->status == 413);

	let form = await fetch("http://" + address + "/form", {
		method: "POST", body: "account[name]=Grace", headers: { "Content-Type": "application/x-www-form-urlencoded" }
	});
	check("extended form parsing", form->status == 200 && await form->text() == "Grace");
	let unsupported = await fetch("http://" + address + "/unsupported-parser", {
		method: "POST", body: "plain", headers: { "Content-Type": "text/plain" }
	});
	check("unmatched parser does not claim JSON", await unsupported->text() == "false");
	let issued = await fetch("http://" + address + "/signed");
	let signedCookie = issued->headers["set-cookie"];
	let signedCheck = await fetch("http://" + address + "/signed-check", { headers: { Cookie: signedCookie } });
	check("signed cookie verification", await signedCheck->text() == "production-session");
	let tampered = await fetch("http://" + address + "/signed-check", {
		headers: { Cookie: "session=s:production-session.invalid-signature" }
	});
	check("tampered signed cookie rejected", await tampered->text() == "false");

	let validator = await fetch("http://" + address + "/validator");
	let etag = validator->headers["etag"];
	let conditional = await fetch("http://" + address + "/validator", { headers: { "If-None-Match": etag } });
	check("strong ETag conditional request", etag != null && conditional->status == 304);
	let head = await fetch("http://" + address + "/validator", { method: "HEAD" });
	check("HEAD keeps representation length without body", head->status == 200 && await head->text() == "");
	let range = await fetch("http://" + address + "/file", { headers: { Range: "bytes=0-4" } });
	check("file range response", range->status == 206 && await range->text() == "safe ");

	let method = await fetch("http://" + address + "/health", { method: "POST" });
	check("production 405 and Allow", method->status == 405 && method->headers["allow"] != null);
	let options = await fetch("http://" + address + "/health", { method: "OPTIONS" });
	check("automatic OPTIONS", options->status == 204 && options->headers["allow"] != null);
	let callbackControl = await fetch("http://" + address + "/callback-control");
	check("nested callback control", callbackControl->status == 200 && await callbackControl->json() == 2);
	let format = await fetch("http://" + address + "/format", { headers: { Accept: "text/html" } });
	check("res.format callback dispatch", format->status == 200 && await format->text() == "format response");
	let formatMiss = await fetch("http://" + address + "/format", { headers: { Accept: "application/json" } });
	check("res.format rejects unmatched representations", formatMiss->status == 406);
	let stream = await fetch("http://" + address + "/stream");
	check("streamed response flush/end", stream->status == 200 && await stream->text() == "first:last");
	let mounted = await fetch("http://" + address + "/teams/blue/member/17");
	check("mounted router params", await mounted->text() == "blue:17");
	let escaped = await fetch("http://" + address + "/teams/red/escape");
	check("next router returns to parent", await escaped->text() == "parent:red");
	let liveRoute = await fetch("http://" + address + "/teams/green/added-after-mount");
	check("mounted router sees later routes", await liveRoute->text() == "live-router-route");

	app->close();
	if (failures != 0) {
		throw "HTTP production smoke failed";
	}
	println("PASS: HTTP production smoke");
};
run();
