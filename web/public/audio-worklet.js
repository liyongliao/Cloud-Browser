/* 48 kHz stereo s16le input; linearly resample to the output device rate. */
class PCMPlayer extends AudioWorkletProcessor {
  constructor() {
    super();
    this.frames = [];
    this.offset = 0;
    this.queued = 0;
    this.port.onmessage = ({ data }) => {
      if (data === "reset") {
        this.frames = [];
        this.offset = 0;
        this.queued = 0;
        return;
      }
      const b = new Int16Array(data);
      if (b.length % 2) return;
      this.frames.push(b);
      this.queued += b.length / 2;
      while (this.queued > 12000 && this.frames.length > 1) {
        this.queued -= this.frames.shift().length / 2;
        this.offset = 0;
      }
    };
  }
  process(inputs, outputs) {
    const output = outputs[0];
    if (!output?.length) return true;
    const step = 48000 / sampleRate;
    for (let n = 0; n < output[0].length; n++) {
      while (this.frames.length && this.offset >= this.frames[0].length / 2) {
        const count = this.frames[0].length / 2;
        this.frames.shift();
        this.queued -= count;
        this.offset -= count;
      }
      if (!this.frames.length) {
        this.offset = 0;
        break;
      }
      const b = this.frames[0],
        i = Math.floor(this.offset),
        fraction = this.offset - i;
      for (let ch = 0; ch < output.length; ch++) {
        const channel = Math.min(ch, 1);
        const a = b[i * 2 + channel] / 32768;
        const next =
          i + 1 < b.length / 2
            ? b[(i + 1) * 2 + channel] / 32768
            : (this.frames[1]?.[channel] ?? b[i * 2 + channel]) / 32768;
        output[ch][n] = a + (next - a) * fraction;
      }
      this.offset += step;
    }
    return true;
  }
}
registerProcessor("pcm-player", PCMPlayer);
