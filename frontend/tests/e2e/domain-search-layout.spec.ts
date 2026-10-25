import { expect, test, type APIRequestContext, type Locator, type Page } from './fixtures';
import { API_BASE, adminToken, authHeaders, newApi, uniqueDomainLabel } from './helpers';

/**
 * Layout of the /order/domain result list. Each result is one grid row:
 *
 *   wide card   : [ name + badges ] [ price ] [ action ]   options underneath
 *   narrow card : [ name + badges ]
 *                 [ price ]          [ action ]            options underneath
 *
 * "Wide" vs "narrow" follows the card's own width (container query), not the
 * viewport - the store sidebar takes ~260px between 768 and 991px. These specs
 * pin the invariants that made the old flex-wrap markup look ragged: the price
 * and action columns line up across rows, nothing spills out of the card, and
 * the page never scrolls sideways.
 */

interface Box {
	x: number;
	y: number;
	width: number;
	height: number;
}

async function box(locator: Locator): Promise<Box> {
	const b = await locator.boundingBox();
	if (!b) throw new Error(`no bounding box for ${locator}`);
	return b;
}

const right = (b: Box) => b.x + b.width;
const bottom = (b: Box) => b.y + b.height;
const midY = (b: Box) => b.y + b.height / 2;

async function openResults(page: Page, query: string): Promise<Locator> {
	await page.goto(`/order/domain?q=${encodeURIComponent(query)}`);
	await page.waitForLoadState('networkidle');
	await expect(page.getByTestId('domain-results')).toBeVisible({ timeout: 15_000 });
	const rows = page.locator('[data-testid^="domain-result-"]');
	await expect(rows.first()).toBeVisible();
	return rows;
}

async function expectNoSidewaysScroll(page: Page) {
	const overflow = await page.evaluate(
		() => document.documentElement.scrollWidth - document.documentElement.clientWidth
	);
	expect(overflow, 'page scrolls sideways').toBeLessThanOrEqual(0);
}

/** Every visible descendant of every row stays inside the results card. */
async function expectRowsInsideCard(page: Page, rows: Locator) {
	const card = await box(page.getByTestId('domain-results'));
	const n = await rows.count();
	for (let i = 0; i < n; i++) {
		const row = rows.nth(i);
		for (const part of [
			'.ca-dom-name',
			'.ca-dom-price',
			'.ca-dom-action .ca-btn',
			'.ca-dom-opts'
		]) {
			const el = row.locator(part);
			if ((await el.count()) === 0) continue;
			const b = await box(el.first());
			expect(b.x, `${part} of row ${i} leaves the card (left)`).toBeGreaterThanOrEqual(card.x - 1);
			expect(right(b), `${part} of row ${i} leaves the card (right)`).toBeLessThanOrEqual(
				right(card) + 1
			);
		}
	}
}

test.describe('domain search result layout', () => {
	// A search lists the exact name plus one row per active TLD, so a fresh
	// database (one TLD) would show a single row and the cross-row assertions
	// would have nothing to compare. Provision three TLDs of our own.
	let api: APIRequestContext;
	let admin: string;
	const tldIds: number[] = [];

	test.beforeAll(async () => {
		api = await newApi();
		admin = await adminToken(api);
		const regs = await api.get(`${API_BASE}/api/v1/admin/registrars`, {
			headers: authHeaders(admin)
		});
		const registrarId = ((await regs.json()).data as { id: number }[])[0].id;
		for (let i = 0; i < 3; i++) {
			const res = await api.post(`${API_BASE}/api/v1/admin/tld-pricing`, {
				headers: authHeaders(admin),
				data: {
					tld: uniqueDomainLabel(),
					registrar_id: registrarId,
					active: true,
					min_years: 1,
					max_years: 2,
					register_prices: { '1': 100000, '2': 180000 },
					renew_prices: { '1': 110000 },
					transfer_price: 90000
				}
			});
			expect(res.ok(), `create tld pricing: ${await res.text()}`).toBeTruthy();
			tldIds.push(((await res.json()).data as { id: number }).id);
		}
	});

	test.afterAll(async () => {
		for (const id of tldIds) {
			await api.delete(`${API_BASE}/api/v1/admin/tld-pricing/${id}`, {
				headers: authHeaders(admin)
			});
		}
		await api?.dispose();
	});

	test.describe('desktop', () => {
		test.use({ viewport: { width: 1280, height: 900 } });

		test('name, price and action share one line; columns line up across rows', async ({ page }) => {
			const rows = await openResults(page, `${uniqueDomainLabel()}.com`);
			const n = await rows.count();
			expect(n).toBeGreaterThan(1);

			const actionX: number[] = [];
			const actionW: number[] = [];
			const priceRight: number[] = [];
			for (let i = 0; i < n; i++) {
				const row = rows.nth(i);
				const name = await box(row.locator('.ca-dom-name'));
				const price = await box(row.locator('.ca-dom-price'));
				const action = await box(row.locator('.ca-dom-action .ca-btn'));

				// one line: the button is vertically centred on the name
				expect(
					Math.abs(midY(action) - midY(name)),
					`row ${i} button off the name line`
				).toBeLessThan(6);
				expect(Math.abs(midY(price) - midY(name)), `row ${i} price off the name line`).toBeLessThan(
					6
				);
				// left to right: name, price, action - no overlap
				expect(right(name)).toBeLessThanOrEqual(price.x + 1);
				expect(right(price)).toBeLessThanOrEqual(action.x + 1);

				actionX.push(action.x);
				actionW.push(action.width);
				priceRight.push(right(price));
			}

			// every row's button / price column sits at the same x
			for (let i = 1; i < n; i++) {
				expect(actionX[i]).toBeCloseTo(actionX[0], 0);
				expect(actionW[i]).toBeCloseTo(actionW[0], 0);
				expect(priceRight[i]).toBeCloseTo(priceRight[0], 0);
			}

			// the period selector sits on its own line below the main line
			const first = rows.first();
			const years = await box(first.locator('[data-testid^="domain-years-"]'));
			const action = await box(first.locator('.ca-dom-action .ca-btn'));
			expect(years.y).toBeGreaterThanOrEqual(bottom(action));

			await expectRowsInsideCard(page, rows);
			await expectNoSidewaysScroll(page);
		});

		test('search form spans the same width as the results card', async ({ page }) => {
			await openResults(page, `${uniqueDomainLabel()}.com`);
			const form = await box(page.getByTestId('domain-search-form'));
			const card = await box(page.getByTestId('domain-results'));
			expect(form.x).toBeCloseTo(card.x, 0);
			expect(right(form)).toBeCloseTo(right(card), 0);
			const input = await box(page.getByTestId('domain-search-input'));
			const submit = await box(page.getByTestId('domain-search-submit'));
			expect(Math.abs(midY(input) - midY(submit))).toBeLessThan(2);
			expect(right(submit)).toBeCloseTo(right(card), 0);
		});

		test('an unavailable name keeps the same layout with its transfer button', async ({ page }) => {
			const rows = await openResults(page, `${uniqueDomainLabel()}-taken.com`);
			const row = rows.first();
			const transfer = row.locator('[data-testid^="domain-transfer-"]');
			await expect(transfer).toBeVisible();
			const name = await box(row.locator('.ca-dom-name'));
			const button = await box(transfer);
			expect(Math.abs(midY(button) - midY(name))).toBeLessThan(6);
			await expectRowsInsideCard(page, rows);
		});
	});

	test.describe('tablet (store sidebar squeezes the card)', () => {
		test.use({ viewport: { width: 820, height: 1000 } });

		test('rows stay inside the card', async ({ page }) => {
			const rows = await openResults(page, `${uniqueDomainLabel()}.com`);
			await expectRowsInsideCard(page, rows);
			await expectNoSidewaysScroll(page);
		});
	});

	for (const width of [375, 320]) {
		test.describe(`mobile ${width}px`, () => {
			test.use({ viewport: { width, height: 800 } });

			test('name, then price beside the button, then options - nothing overflows', async ({
				page
			}) => {
				const rows = await openResults(page, `${uniqueDomainLabel()}.com`);
				const n = await rows.count();
				expect(n).toBeGreaterThan(1);

				for (let i = 0; i < n; i++) {
					const row = rows.nth(i);
					const name = await box(row.locator('.ca-dom-name'));
					const price = await box(row.locator('.ca-dom-price'));
					const action = await box(row.locator('.ca-dom-action .ca-btn'));

					// stacked: name above the price/button line
					expect(price.y).toBeGreaterThanOrEqual(bottom(name) - 1);
					// price and button share a line, price on the left
					expect(
						Math.abs(midY(price) - midY(action)),
						`row ${i} price/button misaligned`
					).toBeLessThan(6);
					expect(right(price)).toBeLessThanOrEqual(action.x + 1);
					// options below
					const years = await box(row.locator('[data-testid^="domain-years-"]'));
					expect(years.y).toBeGreaterThanOrEqual(bottom(action));
				}

				await expectRowsInsideCard(page, rows);
				await expectNoSidewaysScroll(page);
			});

			test('the first result can still be added to the cart', async ({ page }) => {
				const rows = await openResults(page, `${uniqueDomainLabel()}.com`);
				const register = rows.first().locator('[data-testid^="domain-register-"]');
				await register.click();
				await expect(rows.first().locator('[data-testid^="domain-in-cart-"]')).toBeVisible();
				// in-cart rows drop the option controls and keep a tidy two-line layout
				await expect(rows.first().locator('.ca-dom-opts')).toHaveCount(0);
				await expectRowsInsideCard(page, rows);
				await expectNoSidewaysScroll(page);
			});
		});
	}
});
