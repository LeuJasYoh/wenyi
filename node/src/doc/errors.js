// 输入层异常体系（主规格 §7.5 / 分册 03 §1.3）。
export class IngestError extends Error {
  constructor(message) {
    super(message);
    this.name = "IngestError";
  }
}

export class MinerUError extends IngestError {
  constructor(message) {
    super(message);
    this.name = "MinerUError";
  }
}

export class MinerUTimeoutError extends MinerUError {
  constructor(message) {
    super(message);
    this.name = "MinerUTimeoutError";
  }
}
