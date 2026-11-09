import { Express, fetch } from "core:http";
import { println } from "core:io";

const app = Express();
app->set("body limit", 8);
app->set("env", "test");
app->set("feature flag", "configured");
app->enable("405");
app->set("case sensitive routing", true);
app->set("strict routing", true);
app->set("x-powered-by", false);
app->set("query parser", "extended");
app->set("json spaces", 2);
	app->set("json escape", true);
var address = "";
app->get("/text", "fetch text works");
app->get("/static-metadata", "static body");
app->get("/length", "length check");
app->get("/strong-etag", fn(req, res) {
	res->send("strong validator");
});
app->get("/modified", fn(req, res) {
	res->set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT");
	res->send("modified body");
});
app->get("/only-get", "get only");
app->route("/chained")->get("chained get")->post("chained post");
app->get(["/alias-a", "/alias-b"], "array route");
app->get("/strict/", "strict route");
app->get("/Case", "case route");
app->get("/settings", fn(req, res) {
	res->json({
		feature: app->get("feature flag"),
		enabled: app->enabled("405"),
		disabled: app->disabled("405")
	});
});
app->get("/route-info", fn(req, res) {
	if (req->route->methods->get) {
		res->send(req->route->path + ":get");
	} else {
		res->send("missing route method");
	}
});
app->get("/request-links", fn(req, res) {
	req->res->set("X-Request-Response", "present");
	res->send(req->app->get("feature flag"));
});
app->get("/locals", fn(req, res) {
	app->locals->shared = "app";
	res->locals->request = "response";
	res->send(app->locals->shared + ":" + res->locals->request);
});
app->get("/status-after-send", fn(req, res) {
	res->send("already sent");
	res->status(418);
});
app->get("/json", "{\"result\":\"fetch json works\"}");
app->get("/json-spaces", fn(req, res) {
	res->json({ name: "Ada", active: true });
});
app->get("/json-escape", fn(req, res) {
	res->json({ value: "<tag>&" });
});
app->get("/jsonp", fn(req, res) {
	res->jsonp({ value: "ok" });
});
app->get("/attachment", fn(req, res) {
	res->attachment("report.txt")->send("download body");
});
app->get("/headers-sent", fn(req, res) {
	if (res->headersSent == false) {
		res->send("false:true");
	} else {
		res->send("unexpected");
	}
});
app->get("/file", fn(req, res) {
	res->sendFile("http-file-fixture.txt", { root: "../tests" });
});
app->get("/download", fn(req, res) {
	res->download("http-file-fixture.txt", "renamed.txt", { root: "../tests" });
});
app->get("/file-traversal", fn(req, res) {
	res->sendFile("../readme.md", { root: "../tests" });
});
app->get("/request-info", fn(req, res) {
	res->json({
		first: req->query->x[0],
		second: req->query->x[1],
		raw: req->rawQuery,
		protocol: req->protocol,
		secure: req->secure,
		xhr: req->xhr,
		headerValues: req->getAll("X-Multi")
	});
});
app->get("/range", fn(req, res) {
	let ranges = req->range(10);
	res->json({ ranges: ranges });
});
app->get("/extended-query", fn(req, res) {
	res->send(req->query->user->name + ":" + req->query->tags[0] + ":" + req->query->tags[1]);
});
app->get("/response-headers", fn(req, res) {
	res->append("Set-Cookie", ["a=1", "b=2"]);
	res->links({ next: "/next", prev: "/prev" });
	res->vary("Accept")->vary("Accept-Language");
	res->location("/destination");
	res->send("headers ready");
});
app->get("/users/:id(\\d+)", fn(req, res) {
	res->send(req->params->id);
});
app->get("/param-decode/:value", fn(req, res) {
	res->send(req->params->value);
});
var paramCount = 0;
app->param("id", fn(req, res, next, value, name) {
	paramCount = paramCount + 1;
	if (name == "id" && value == req->params->id) {
		next();
	} else {
		next("invalid param callback args");
	}
});
app->param("id", fn(req, res, next, value, name) {
	paramCount = paramCount + 1;
	next();
});
app->param("asyncId", fn(req, res, next, value, name) async {
	await fetch("http://" + address + "/text");
	next();
});
app->get("/param-once/:id", fn(req, res, next) {
	next();
}, fn(req, res) {
	res->send(paramCount);
});
app->get("/param-async/:asyncId", fn(req, res) {
	res->send(req->params->asyncId);
});
app->get("/optional/:id?", fn(req, res) {
	res->send(req->params->id);
});
app->get("/repeated/:parts+", fn(req, res) {
	res->send(req->params->parts);
});
app->get("/repeated-optional/:parts*", fn(req, res) {
	res->send(req->params->parts);
});
app->get("/files/*/edit", fn(req, res) {
	res->send(req->params["0"]);
});
app->get("/async", fn(req, res) async {
	await fetch("http://" + address + "/text");
	res->send("async route completed");
});
app->get("/async-error", fn(req, res) async {
	await fetch("://invalid-handler-url");
	res->send("unreachable");
});
app->get("/skip", fn(req, res, next) {
	next("route");
}, fn(req, res) {
	res->send("wrong route handler");
});
app->get("/skip", "route skipped");
app->get("/after-next", fn(req, res, next) {
	next();
	res->send("wrong early response");
}, fn(req, res) {
	res->send("downstream response");
});
app->get("/throw", fn(req, res) {
	throw "route failure";
});
app->use(fn(err, req, res, next) {
	res->status(500)->send("middleware handled: " + err);
});
app->onError(fn(err, req, res, next) {
	res->status(502)->send("async handler failed: " + err);
});
app->post("/echo", fn(req, res) {
	res->send(req->body);
});
app->post("/request-content", fn(req, res) {
	if (req->get("Content-Type") == "application/json" && req->is("json")) {
		res->send("json content recognized");
	} else {
		res->send("content not recognized");
	}
});
app->get("/accepts", fn(req, res) {
	res->send(req->accepts(["json", "html"]));
});
app->get("/accept-excluded", fn(req, res) {
	res->send(req->accepts(["json", "html"]));
});
app->get("/accept-tokens", fn(req, res) {
	res->send(req->acceptsCharsets(["utf-8", "iso-8859-1"]) + ":" +
		req->acceptsEncodings(["gzip", "br"]) + ":" +
		req->acceptsLanguages(["en", "fr"]));
});
app->get("/cookies", fn(req, res) {
	res->cookie("session", "hello world", { path: "/", httpOnly: true, sameSite: "lax", maxAge: 60000 });
	res->clearCookie("old", { path: "/" });
	res->send(req->cookies->incoming);
});
app->get("/error", fn(req, res) {
	res->status(500)->send("Internal Server Error");
});
address = app->listen("127.0.0.1:0");

var checkFetch = fn() async {
	println("settings:", app->get("feature flag"), app->enabled("405"), app->disabled("405"));
	let settingsResponse = await fetch("http://" + address + "/settings");
	let settings = await settingsResponse->json();
	println("settings response:", settings->feature, settings->enabled, settings->disabled);
	println("route metadata:", await (await fetch("http://" + address + "/route-info"))->text());
	let backrefResponse = await fetch("http://" + address + "/request-links");
	println("request backrefs:", await backrefResponse->text(), backrefResponse->headers["x-request-response"]);
	println("extended query:", await (await fetch("http://" + address + "/extended-query?user[name]=Ada&tags[]=red&tags[]=blue"))->text());
	println("locals:", await (await fetch("http://" + address + "/locals"))->text());
	let sentStatusResponse = await fetch("http://" + address + "/status-after-send");
	println("status after send:", sentStatusResponse->status, await sentStatusResponse->text());
	let textResponse = await fetch("http://" + address + "/text");
	println(textResponse->status);
	println(await textResponse->text());
	println("powered by disabled:", textResponse->headers["x-powered-by"]);
	app->set("x-powered-by", true);
	let staticResponse = await fetch("http://" + address + "/static-metadata");
	let staticConditional = await fetch("http://" + address + "/static-metadata", {
		headers: { "If-None-Match": staticResponse->headers["etag"] }
	});
	println("static metadata:", staticResponse->headers["x-powered-by"], staticResponse->headers["content-length"], staticConditional->status);
	app->set("x-powered-by", false);
	println("json spaces:", await (await fetch("http://" + address + "/json-spaces"))->text());
		println("json escape:", await (await fetch("http://" + address + "/json-escape"))->text());
			let jsonpResponse = await fetch("http://" + address + "/jsonp?callback=app.done");
			let unsafeJSONPResponse = await fetch("http://" + address + "/jsonp?callback=alert(1)");
			println("jsonp:", jsonpResponse->headers["content-type"], await jsonpResponse->text(), unsafeJSONPResponse->headers["content-type"], await unsafeJSONPResponse->text());
		let attachmentResponse = await fetch("http://" + address + "/attachment");
		println("attachment:", attachmentResponse->headers["content-disposition"], attachmentResponse->headers["content-type"], await attachmentResponse->text());
		println("headers sent:", await (await fetch("http://" + address + "/headers-sent"))->text());
		let fileResponse = await fetch("http://" + address + "/file");
		let downloadResponse = await fetch("http://" + address + "/download");
		let traversalResponse = await fetch("http://" + address + "/file-traversal");
		println("file responses:", fileResponse->status, await fileResponse->text(), fileResponse->headers["last-modified"], downloadResponse->headers["content-disposition"], traversalResponse->status);
	app->set("x-powered-by", true);
	let poweredResponse = await fetch("http://" + address + "/text");
	println("powered by enabled:", poweredResponse->headers["x-powered-by"]);
	let lengthResponse = await fetch("http://" + address + "/length");
	println("content length:", lengthResponse->headers["content-length"], await lengthResponse->text());
	app->set("etag", "strong");
	let strongETagResponse = await fetch("http://" + address + "/strong-etag");
	app->set("etag", "weak");
	println("strong etag:", strongETagResponse->headers["etag"]);
	let etag = lengthResponse->headers["etag"];
	let conditionalResponse = await fetch("http://" + address + "/length", {
		headers: { "If-None-Match": etag }
	});
	println("conditional get:", conditionalResponse->status, await conditionalResponse->text());
	let modifiedResponse = await fetch("http://" + address + "/modified", {
		headers: { "If-Modified-Since": "Wed, 21 Oct 2015 07:28:00 GMT" }
	});
	let olderResponse = await fetch("http://" + address + "/modified", {
		headers: { "If-Modified-Since": "Tue, 20 Oct 2015 07:28:00 GMT" }
	});
	println("if-modified-since:", modifiedResponse->status, await modifiedResponse->text(), olderResponse->status);
	let headLengthResponse = await fetch("http://" + address + "/length", { method: "HEAD" });
	println("head length:", headLengthResponse->headers["content-length"], await headLengthResponse->text());
	println("route chain get:", await (await fetch("http://" + address + "/chained"))->text());
	let chainedPost = await fetch("http://" + address + "/chained", { method: "POST" });
	println("route chain post:", await chainedPost->text());
	println("array route:", await (await fetch("http://" + address + "/alias-b"))->text());
	let strictMissing = await fetch("http://" + address + "/strict");
	let strictPresent = await fetch("http://" + address + "/strict/");
	println("strict slash:", strictMissing->status, strictPresent->status);
	let caseMissing = await fetch("http://" + address + "/case");
	let casePresent = await fetch("http://" + address + "/Case");
	println("case route:", caseMissing->status, casePresent->status);
	let methodResponse = await fetch("http://" + address + "/only-get", { method: "POST" });
	println("method not allowed:", methodResponse->status, methodResponse->headers->allow);
	let asyncResponse = await fetch("http://" + address + "/async");
	println("async route:", await asyncResponse->text());
	let asyncErrorResponse = await fetch("http://" + address + "/async-error");
	println("async route error:", asyncErrorResponse->status, await asyncErrorResponse->text());
	println("constrained param:", await (await fetch("http://" + address + "/users/42"))->text());
	println("param decode:", await (await fetch("http://" + address + "/param-decode/hello%20world"))->text());
	println("param decode once:", await (await fetch("http://" + address + "/param-decode/%252F"))->text());
	paramCount = 0;
		println("param callback once:", await (await fetch("http://" + address + "/param-once/7"))->text());
		println("async param callback:", await (await fetch("http://" + address + "/param-async/ok"))->text());
	println("optional param:", await (await fetch("http://" + address + "/optional"))->text());
	println("repeated param:", await (await fetch("http://" + address + "/repeated/a/b/c"))->text());
	println("optional repeated param:", await (await fetch("http://" + address + "/repeated-optional"))->text());
	println("middle wildcard:", await (await fetch("http://" + address + "/files/a/b/edit"))->text());
	let infoResponse = await fetch("http://" + address + "/request-info?x=one&x=two", {
		headers: { "X-Multi": "first, second", "X-Requested-With": "XMLHttpRequest" }
	});
	let info = await infoResponse->json();
	println("request metadata:", info->first, info->second, info->raw, info->protocol, info->secure, info->xhr);
		let validRange = await fetch("http://" + address + "/range", { headers: { Range: "bytes=2-20" } });
		let invalidRange = await fetch("http://" + address + "/range", { headers: { Range: "items=2-4" } });
		let unsatisfiableRange = await fetch("http://" + address + "/range", { headers: { Range: "bytes=20-30" } });
		let validRanges = await validRange->json();
		let invalidRanges = await invalidRange->json();
		let unsatisfiableRanges = await unsatisfiableRange->json();
		println("ranges:", validRanges->ranges[0]->start, validRanges->ranges[0]->end,
			invalidRanges->ranges, unsatisfiableRanges->ranges);
	let headersResponse = await fetch("http://" + address + "/response-headers");
	println("response headers:", headersResponse->headers["set-cookie"], headersResponse->headers->link, headersResponse->headers->vary, headersResponse->headers->location);
	let skipResponse = await fetch("http://" + address + "/skip");
	println("next route:", await skipResponse->text());
	let afterNextResponse = await fetch("http://" + address + "/after-next");
	println("send after next:", await afterNextResponse->text());
	let thrownResponse = await fetch("http://" + address + "/throw");
	println("error middleware:", await thrownResponse->text());

	let jsonResponse = await fetch("http://" + address + "/json");
	let data = await jsonResponse->json();
	println(data->result);

	let postResponse = await fetch("http://" + address + "/echo", {
		method: "POST",
		body: "posted",
		headers: { "Content-Type": "text/plain" }
	});
	println(await postResponse->text());
	let contentResponse = await fetch("http://" + address + "/request-content", {
		method: "POST",
		body: "{}",
		headers: { "Content-Type": "application/json" }
	});
	println("request content:", await contentResponse->text());
	let acceptsResponse = await fetch("http://" + address + "/accepts", {
		headers: { Accept: "text/html;q=0.9, application/json;q=0.2" }
	});
	println("accepts:", await acceptsResponse->text());
	let excludedResponse = await fetch("http://" + address + "/accept-excluded", {
		headers: { Accept: "application/json;q=0, */*;q=1" }
	});
	println("accept exclusion:", await excludedResponse->text());
	let tokenResponse = await fetch("http://" + address + "/accept-tokens", {
		headers: {
			"Accept-Charset": "utf-8;q=0.3, iso-8859-1;q=0.8",
			"Accept-Encoding": "gzip;q=0.2, br;q=0.9",
			"Accept-Language": "en;q=0.4, fr;q=0.9"
		}
	});
	println("accept tokens:", await tokenResponse->text());
	let cookieResponse = await fetch("http://" + address + "/cookies", {
		headers: { Cookie: "incoming=from%20browser" }
	});
	println("cookies:", await cookieResponse->text(), cookieResponse->headers["set-cookie"]);
	let oversizedResponse = await fetch("http://" + address + "/echo", {
		method: "POST",
		body: "this body exceeds eight bytes"
	});
	println("body limit:", oversizedResponse->status);

	let errorResponse = await fetch("http://" + address + "/error");
	let errorBody = await errorResponse->text();
	println("500 status:", errorResponse->status);
	println("500 ok:", errorResponse->ok);
	println("500 body:", errorBody);
	println("PASS: received HTTP error response without rejecting fetch");
	try {
		await fetch("://invalid-url");
		println("FAIL: invalid fetch unexpectedly resolved");
	} catch (err) {
		println("FETCH CAUGHT:", err->message);
	}
	try {
		let caughtPromise = fetch("://invalid-promise-catch")->catch(fn(err) {
			println("PROMISE CATCH:", err);
			return "handled";
		});
		let catchResult = await caughtPromise;
		println("PROMISE CATCH RESULT:", catchResult);
	} catch (err) {
		println("FAIL: promise catch escaped to try/catch:", err->message);
	}
	app->close();
};

checkFetch();