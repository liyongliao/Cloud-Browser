export async function startAudio(
  path: string,
  onState: (state: string) => void,
): Promise<() => void> {
  const context = new AudioContext();
  await context.resume();
  try {
    await context.audioWorklet.addModule("/audio-worklet.js");
  } catch (error) {
    await context.close();
    throw error;
  }
  const node = new AudioWorkletNode(context, "pcm-player", {
    numberOfInputs: 0,
    numberOfOutputs: 1,
    outputChannelCount: [2],
  });
  node.connect(context.destination);
  let stopped = false,
    attempt = 0,
    socket: WebSocket | undefined,
    timer: number | undefined;
  function connect() {
    if (stopped) return;
    socket = new WebSocket(
      `${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}${path}`,
    );
    socket.binaryType = "arraybuffer";
    socket.onopen = () => {
      attempt = 0;
      onState("声音已开启");
    };
    socket.onmessage = (e) => {
      if (e.data instanceof ArrayBuffer)
        node.port.postMessage(e.data, [e.data]);
    };
    socket.onclose = () => {
      node.port.postMessage("reset");
      if (!stopped) {
        onState("声音断开，正在重连…");
        if (attempt++ < 5)
          timer = window.setTimeout(
            connect,
            Math.min(1000 * 2 ** attempt, 10000),
          );
        else onState("声音连接失败，请关闭后重试");
      }
    };
  }
  connect();
  return () => {
    stopped = true;
    clearTimeout(timer);
    socket?.close();
    node.disconnect();
    void context.close();
  };
}
