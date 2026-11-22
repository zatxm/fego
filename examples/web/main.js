async function start() {
  const go = new Go();
  const params = new URLSearchParams(location.search);
  const video = params.get("video");
  if (video) go.argv.push("-video", video);
  if (params.has("dark")) go.argv.push("-dark");
  const response = await fetch("app.wasm");
  if (!response.ok) throw new Error(`WASM download failed (${response.status})`);
  const { instance } = response.headers.get("content-type") === "application/wasm"
    ? await WebAssembly.instantiateStreaming(response, go.importObject)
    : await WebAssembly.instantiate(await response.arrayBuffer(), go.importObject);
  document.getElementById("status").hidden = true;
  await go.run(instance);
}

start().catch((error) => {
  const status = document.getElementById("status");
  status.hidden = false;
  status.textContent = error.message;
});
