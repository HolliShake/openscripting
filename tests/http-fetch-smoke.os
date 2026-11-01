import { Express, fetch } from "core:http";
import { println } from "core:io";

const app = Express();
app->get("/text", "fetch text works");
app->get("/json", "{\"result\":\"fetch json works\"}");
app->post("/echo", fn(req, res) {
	res->send(req->body);
});
app->get("/error", fn(req, res) {
	res->status(500)->send("Internal Server Error");
});
var address = app->listen("127.0.0.1:0");

var checkFetch = fn() async {
	let textResponse = await fetch("http://" + address + "/text");
	println(textResponse->status);
	println(await textResponse->text());

	let jsonResponse = await fetch("http://" + address + "/json");
	let data = await jsonResponse->json();
	println(data->result);

	let postResponse = await fetch("http://" + address + "/echo", {
		method: "POST",
		body: "posted body",
		headers: { "Content-Type": "text/plain" }
	});
	println(await postResponse->text());

	let errorResponse = await fetch("http://" + address + "/error");
	let errorBody = await errorResponse->text();
	println("500 status:", errorResponse->status);
	println("500 ok:", errorResponse->ok);
	println("500 body:", errorBody);
	println("PASS: received HTTP error response without rejecting fetch");
	app->close();
};

checkFetch();