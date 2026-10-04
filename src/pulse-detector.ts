export interface Sample {
  time: number;
  value: number;
}

export interface PulseEvent {
  id: number;
  start: number;
  end: number;
  peak: number;
  peakTime: number;
  integral: number;
  complete: boolean;
}

export interface DetectorOptions {
  highThreshold: number;
  lowThreshold: number;
  maxGap: number;
}

interface OpenEvent {
  id: number;
  start: number;
  peak: number;
  peakTime: number;
  integral: number;
  lastGoodTime: number;
  gapAccum: number;
  gapStart: number;
  gapIntegral: number;
  inGap: boolean;
}

export class PulseDetector {
  private readonly high: number;
  private readonly low: number;
  private readonly maxGap: number;
  private samples: Sample[] = [];
  private events: PulseEvent[] = [];
  private open: OpenEvent | null = null;
  private prev: Sample | null = null;
  private nextId: number = 1;
  private flushed: boolean = false;

  constructor(options: DetectorOptions) {
    if (!(options.lowThreshold <= options.highThreshold)) {
      throw new Error('lowThreshold must be <= highThreshold');
    }
    if (!(options.maxGap >= 0)) {
      throw new Error('maxGap must be >= 0');
    }
    this.high = options.highThreshold;
    this.low = options.lowThreshold;
    this.maxGap = options.maxGap;
  }

  get completed(): PulseEvent[] {
    return this.events.slice();
  }

  get pending(): PulseEvent | null {
    if (this.open === null) {
      return null;
    }
    return this.snapshot(this.open, false);
  }

  push(sample: Sample): void {
    this.assertActive();
    if (this.prev !== null && sample.time <= this.prev.time) {
      throw new Error('push requires strictly increasing time; use insert() for late corrections');
    }
    this.samples.push(sample);
    this.step(sample);
  }

  insert(sample: Sample): void {
    this.assertActive();
    for (let i = 0; i < this.samples.length; i++) {
      const existing = this.samples[i];
      if (existing.time === sample.time) {
        this.samples[i] = sample;
        this.recompute();
        return;
      }
      if (existing.time > sample.time) {
        this.samples.splice(i, 0, sample);
        this.recompute();
        return;
      }
    }
    this.samples.push(sample);
    this.recompute();
  }

  flush(): PulseEvent[] {
    this.assertActive();
    this.flushed = true;
    const all = this.events.slice();
    if (this.open !== null) {
      const tail = this.snapshot(this.open, false);
      all.push(tail);
    }
    return all;
  }

  private assertActive(): void {
    if (this.flushed) {
      throw new Error('detector already flushed');
    }
  }

  private beginEvent(s: Sample): void {
    const ev: OpenEvent = {
      id: this.nextId,
      start: s.time,
      peak: s.value,
      peakTime: s.time,
      integral: 0,
      lastGoodTime: s.time,
      gapAccum: 0,
      gapStart: s.time,
      gapIntegral: 0,
      inGap: false,
    };
    this.nextId = this.nextId + 1;
    this.open = ev;
  }

  private closeEvent(ev: OpenEvent): void {
    const done = this.snapshot(ev, true);
    this.events.push(done);
    this.open = null;
  }

  private snapshot(ev: OpenEvent, complete: boolean): PulseEvent {
    const out: PulseEvent = {
      id: ev.id,
      start: ev.start,
      end: ev.lastGoodTime,
      peak: ev.peak,
      peakTime: ev.peakTime,
      integral: ev.integral,
      complete: complete,
    };
    return out;
  }

  private step(s: Sample): void {
    const prev = this.prev;
    if (this.open === null) {
      if (s.value >= this.high) {
        this.beginEvent(s);
      }
    } else {
      const ev = this.open;
      const trap = prev === null ? 0 : ((prev.value + s.value) / 2) * (s.time - prev.time);
      if (!ev.inGap) {
        if (s.value < this.low) {
          ev.inGap = true;
          ev.gapStart = prev === null ? s.time : prev.time;
          ev.gapIntegral = trap;
        } else {
          ev.integral = ev.integral + trap;
          if (s.value > ev.peak) {
            ev.peak = s.value;
            ev.peakTime = s.time;
          }
          ev.lastGoodTime = s.time;
        }
      } else {
        if (s.value >= this.low) {
          const gapDur = s.time - ev.gapStart;
          if (ev.gapAccum + gapDur > this.maxGap) {
            this.closeEvent(ev);
            if (s.value >= this.high) {
              this.beginEvent(s);
            }
          } else {
            ev.gapAccum = ev.gapAccum + gapDur;
            ev.integral = ev.integral + ev.gapIntegral + trap;
            ev.gapIntegral = 0;
            ev.inGap = false;
            if (s.value > ev.peak) {
              ev.peak = s.value;
              ev.peakTime = s.time;
            }
            ev.lastGoodTime = s.time;
          }
        } else {
          ev.gapIntegral = ev.gapIntegral + trap;
        }
      }
      if (this.open === ev && ev.inGap) {
        if (ev.gapAccum + (s.time - ev.gapStart) > this.maxGap) {
          this.closeEvent(ev);
        }
      }
    }
    this.prev = s;
  }

  private recompute(): void {
    const all = this.samples;
    this.events = [];
    this.open = null;
    this.prev = null;
    this.nextId = 1;
    for (const s of all) {
      this.step(s);
    }
  }
}
