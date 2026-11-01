import { Express } from "core:http";
import { println } from "core:io";

const app = Express();
app->get("/hello", fn(req, res) {
	res->send("hello from " + req->method);
});
app->post("/created", fn(req, res) {
	res->status(201)->send("received " + req->body);
});
app->get("/json", fn(req, res) {
	res->json({ path: req->path, query: req->query });
});
println(app->listen("127.0.0.1:18765"));
