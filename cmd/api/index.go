package main

import (
	"fmt"
	"net/http"
)

func (app *application) rootHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprint(w, `
	<!DOCTYPE html>
	<html>
	<head>
		<title>Welcome to Our API</title>
		<style>
			body {
				font-family: Arial, sans-serif;
				margin: 0;
				padding: 0;
				background-color: #f0f0f0;
			}
			.container {
				width: 80%;
				margin: auto;
				padding: 20px;
			}
			h1, h2 {
				color: #333;
			}
			p {
				color: #666;
			}
		</style>
	</head>
	<body>
		<div class="container">
			<h1>Welcome to Our API!</h1>
			<h2>API Endpoints</h2>
			<p><strong>GET /api/v1/healthcheck:</strong> Health of the API, PostgreSQL and Redis.</p>
			<p><strong>POST /api/v1/auth/login, /api/v1/auth/logout:</strong> Admin session.</p>
			<p><strong>POST /api/v1/send:</strong> Queue a tracked email (admin).</p>
			<p><strong>GET /api/v1/sent, /api/v1/recipients/:email:</strong> Delivery history (admin, paginated).</p>
			<p><strong>/api/v1/campaigns/...:</strong> Certificate campaigns (admin).</p>
			<p><strong>GET /api/v1/redirect?id=:</strong> Open tracking.</p>
			<p><strong>GET /debug/vars:</strong> Runtime and outbox metrics (admin).</p>
		</div>
	</body>
	</html>
    `)
}
