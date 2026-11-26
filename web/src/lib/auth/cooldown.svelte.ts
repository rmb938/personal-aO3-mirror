// A rate limit or backoff the user has to wait out (Retry-After): forms keep their submit
// button off while `left` is above 0 and show the countdown.

/** How often the countdown ticks, in ms. */
const tick = 250;

export class Cooldown {
	#until = $state(0);
	#now = $state(Date.now());
	#timer: ReturnType<typeof setInterval> | undefined;

	/** Whole seconds left to wait, 0 once it has passed. */
	get left(): number {
		return Math.max(0, Math.ceil((this.#until - this.#now) / 1000));
	}

	/** Starts (or restarts) a wait of `seconds`. The countdown stops by itself once it passes. */
	start(seconds: number): void {
		this.#now = Date.now();
		this.#until = this.#now + seconds * 1000;
		clearInterval(this.#timer);
		this.#timer = setInterval(() => {
			this.#now = Date.now();
			if (this.#now >= this.#until) {
				this.stop();
			}
		}, tick);
	}

	/** Ends the wait now (also when the page goes away). */
	stop(): void {
		clearInterval(this.#timer);
		this.#timer = undefined;
		this.#until = 0;
	}
}
