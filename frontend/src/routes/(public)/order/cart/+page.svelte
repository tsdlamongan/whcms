<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import { Alert, EmptyState, FormField, MoneyText, Tabs } from '$lib/components';
	import Turnstile from '$lib/components/ca/Turnstile.svelte';
	import { t } from '$lib/i18n';
	import {
		cart,
		cartTotals,
		lineTotal,
		optionsDelta,
		type AppliedCoupon
	} from '$lib/stores/cart.svelte';
	import { toast } from '$lib/stores/toast.svelte';
	import { untrack } from 'svelte';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	$effect(() => {
		cart.init();
	});

	const totals = $derived(cartTotals(cart.items, cart.coupon, data.tax));
	const itemsJson = $derived(JSON.stringify(cart.toOrderPayloadItems()));
	const isClient = $derived(data.user?.role === 'client');

	// Auth panel
	let authTab = $state<'login' | 'register'>('login');
	const authTabs = $derived([
		{ id: 'login', label: t('auth.login') },
		{ id: 'register', label: t('auth.register') }
	]);

	let loginEmail = $state(untrack(() => form?.loginEmail ?? ''));
	let loginPassword = $state('');
	let loggingIn = $state(false);

	let regEmail = $state(untrack(() => form?.registerValues?.email ?? ''));
	let regPassword = $state('');
	let regConfirm = $state('');
	let regFirstName = $state(untrack(() => form?.registerValues?.first_name ?? ''));
	let regLastName = $state(untrack(() => form?.registerValues?.last_name ?? ''));
	let regCompany = $state(untrack(() => form?.registerValues?.company ?? ''));
	let regPhone = $state(untrack(() => form?.registerValues?.phone ?? ''));
	let regAddress = $state(untrack(() => form?.registerValues?.address1 ?? ''));
	let regCity = $state(untrack(() => form?.registerValues?.city ?? ''));
	let regState = $state(untrack(() => form?.registerValues?.state ?? ''));
	let regPostcode = $state(untrack(() => form?.registerValues?.postcode ?? ''));
	let regCountry = $state(untrack(() => form?.registerValues?.country ?? 'ID'));
	let registering = $state(false);

	let couponCode = $state('');
	let applyingCoupon = $state(false);
	let resending = $state(false);
	let checkingOut = $state(false);
	let captchaReset = $state(0);

	const loginError = $derived(
		form?.loginErrorKey ? t(form.loginErrorKey) : (form?.loginErrorMessage ?? null)
	);
	const registerError = $derived(
		form?.registerErrorKey ? t(form.registerErrorKey) : (form?.registerErrorMessage ?? null)
	);
	const checkoutError = $derived(
		form?.checkoutErrorKey ? t(form.checkoutErrorKey) : (form?.checkoutErrorMessage ?? null)
	);
	const checkoutProfileIncomplete = $derived(form?.checkoutErrorKey === 'orderfe.cart.profileIncomplete');

	function itemTitle(index: number): string {
		const item = cart.items[index];
		if (!item) return '';
		if (item.item_type === 'product') return item.product_name ?? '';
		return item.item_type === 'domain_register'
			? t('orderfe.cart.domainRegisterItem')
			: t('orderfe.cart.domainTransferItem');
	}

	function removeItem(uid: string) {
		cart.remove(uid);
		toast.info(t('orderfe.cart.removed'));
	}
</script>

<svelte:head>
	<title>{t('orderfe.cart.title')} — {appName}</title>
</svelte:head>

<h1 class="ca-h1">{t('orderfe.cart.title')}</h1>
<p class="ca-lead">{t('orderfe.cart.subtitle')}</p>

{#if !cart.ready}
	<section class="ca-card">
		<div class="ca-card-body" style="text-align:center;color:#adb5bd;padding:40px 20px;">
			{t('common.loading')}
		</div>
	</section>
{:else if cart.count === 0}
	<section class="ca-card">
		<div class="ca-card-body">
			<EmptyState title={t('orderfe.cart.emptyTitle')} description={t('orderfe.cart.emptyDesc')}>
				{#snippet action()}
					<div style="display:flex;flex-wrap:wrap;gap:8px;justify-content:center;">
						<a href="/order" class="ca-btn ca-btn-primary" data-testid="cart-browse-products">
							{t('orderfe.cart.browse')}
						</a>
						<a href="/order/domain" class="ca-btn ca-btn-default">
							{t('orderfe.catalog.searchDomain')}
						</a>
					</div>
				{/snippet}
			</EmptyState>
		</div>
	</section>
{:else}
	<div class="grid grid-cols-1 gap-6 lg:grid-cols-3">
		<div class="space-y-6 lg:col-span-2">
			<div data-testid="cart-items" style="overflow-x:auto;">
				<table class="ca-cart-table">
					<thead>
						<tr>
							<th>{t('orderfe.cart.itemHeader')}</th>
							<th>{t('orderfe.cart.cycleHeader')}</th>
							<th style="text-align:right;">{t('orderfe.cart.priceHeader')}</th>
						</tr>
					</thead>
					<tbody>
						{#each cart.items as item, index (item.uid)}
							<tr data-testid={`cart-item-${index}`}>
								<td>
									<div style="font-weight:600;color:#333;">{itemTitle(index)}</div>
									{#if item.domain}
										<div class="ca-muted" style="font-size:0.8rem;">{item.domain}</div>
									{/if}
									{#if item.item_type === 'domain_transfer'}
										<div class="ca-muted" style="font-size:0.8rem;font-style:italic;">
											{item.epp_code ? t('orderfe.cart.eppFilled') : t('orderfe.cart.eppMissing')}
										</div>
									{/if}
									{#if item.domain_years && item.domain_years > 1}
										<div class="ca-muted" style="font-size:0.8rem;">
											{item.domain_years}{t('orderfe.domain.yearSuffix')}
										</div>
									{/if}
									{#if (item.domain_addon_labels ?? []).length > 0}
										<ul class="ca-muted" style="margin:4px 0 0;padding-left:16px;font-size:0.8rem;">
											{#each item.domain_addon_labels ?? [] as addon (addon.name)}
												<li>{addon.name} (<MoneyText amount={addon.price} />)</li>
											{/each}
										</ul>
									{/if}
									{#if (item.option_labels ?? []).length > 0}
										<ul class="ca-muted" style="margin:4px 0 0;padding-left:16px;font-size:0.8rem;">
											{#each item.option_labels ?? [] as label (label.name)}
												<li>
													{label.name}: {label.value}
													{#if label.delta !== 0}
														(<MoneyText amount={label.delta} />)
													{/if}
												</li>
											{/each}
										</ul>
									{/if}
									{#if (item.specs ?? []).length > 0}
										<ul class="ca-muted" style="margin:4px 0 0;padding-left:16px;font-size:0.8rem;">
											{#each item.specs ?? [] as spec (spec.key)}
												<li>
													{spec.label || spec.key}:
													{spec.unlimited
														? t('orderfe.product.specUnlimited')
														: `${spec.qty}${spec.unit === 'gb' ? 'GB' : spec.unit === 'mb' ? 'MB' : ''}`}
													{#if spec.amount !== 0}
														(<MoneyText amount={spec.amount} />)
													{/if}
												</li>
											{/each}
										</ul>
									{/if}
									<button
										type="button"
										style="margin-top:6px;background:none;border:none;padding:0;color:#c43c35;font:inherit;font-size:0.8rem;font-weight:600;cursor:pointer;text-decoration:underline;"
										onclick={() => removeItem(item.uid)}
										data-testid={`cart-item-remove-${index}`}
									>
										{t('orderfe.cart.remove')}
									</button>
								</td>

								<td>{t(`orderfe.cycle.${item.cycle}`)}</td>

								<td style="text-align:right;">
									<MoneyText amount={lineTotal(item)} class="font-semibold" />
									{#if item.setup_fee > 0}
										<div class="ca-muted" style="font-size:0.78rem;">
											{t('orderfe.cart.setupFee')}: <MoneyText amount={item.setup_fee} />
										</div>
									{/if}
									{#if optionsDelta(item) !== 0}
										<div class="ca-muted" style="font-size:0.78rem;">
											{t('orderfe.product.options')}: <MoneyText amount={optionsDelta(item)} />
										</div>
									{/if}
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>

			<div>
				<nav class="ca-tabs">
					<span class="ca-tab active">
						<i class="fas fa-tag" aria-hidden="true"></i>
						{t('orderfe.cart.couponTitle')}
					</span>
				</nav>
				<div class="ca-tabpanel">
					{#if cart.coupon}
						<div style="display:flex;flex-wrap:wrap;align-items:center;gap:12px;">
							<span
								class="ca-badge ca-badge--success"
								data-testid="coupon-applied"
								style="font-size:0.85rem;"
							>
								{cart.coupon.code}
								{#if cart.coupon.type === 'percentage'}
									&nbsp;(&minus;{cart.coupon.value}%)
								{:else}
									&nbsp;(&minus;<MoneyText amount={cart.coupon.value} />)
								{/if}
							</span>
							<button
								type="button"
								style="background:none;border:none;padding:0;color:#c43c35;font:inherit;font-size:0.9rem;font-weight:600;cursor:pointer;text-decoration:underline;"
								onclick={() => cart.setCoupon(null)}
								data-testid="coupon-remove"
							>
								{t('orderfe.cart.removeCoupon')}
							</button>
						</div>
					{:else}
						<form
							method="POST"
							action="?/coupon"
							style="display:flex;flex-wrap:wrap;gap:8px;"
							use:enhance={() => {
								applyingCoupon = true;
								return async ({ result, update }) => {
									applyingCoupon = false;
									if (result.type === 'success' && result.data?.couponApplied) {
										const applied = result.data.couponApplied as AppliedCoupon;
										cart.setCoupon(applied);
										toast.success(t('orderfe.cart.couponApplied', { code: applied.code }));
									}
									await update({ reset: false });
								};
							}}
						>
							<input
								type="text"
								name="code"
								class="ca-input"
								style="flex:1 1 180px;max-width:20rem;text-transform:uppercase;"
								placeholder={t('orderfe.cart.couponPlaceholder')}
								bind:value={couponCode}
								autocomplete="off"
								data-testid="coupon-input"
							/>
							<button
								type="submit"
								class="ca-btn ca-btn-primary"
								disabled={applyingCoupon}
								data-testid="coupon-apply"
							>
								{t('orderfe.cart.applyCoupon')}
							</button>
						</form>
						{#if form?.couponErrorKey}
							<p
								style="margin-top:8px;color:#c43c35;font-size:0.9rem;"
								role="alert"
								data-testid="coupon-error"
							>
								{t(form.couponErrorKey)}
							</p>
						{/if}
					{/if}
				</div>
			</div>
		</div>

		<div class="space-y-6">
			<div class="ca-order-summary">
				<div class="ca-order-summary-head">{t('orderfe.cart.summary')}</div>
				<div class="ca-order-summary-body">
					<div class="ca-summary-row">
						<span>{t('billing.subtotal')}</span>
						<span data-testid="cart-subtotal">
							<MoneyText amount={totals.subtotal} class="font-medium" />
						</span>
					</div>
					{#if totals.discount > 0}
						<div class="ca-summary-row">
							<span>{t('billing.discount')}</span>
							<span data-testid="cart-discount" style="color:var(--ca-success);font-weight:600;">
								&minus;<MoneyText amount={totals.discount} />
							</span>
						</div>
					{/if}
					{#if data.tax.enabled}
						<div class="ca-summary-row">
							<span>{t('orderfe.cart.taxEstimate', { rate: totals.taxRate })}</span>
							<span data-testid="cart-tax">
								<MoneyText amount={totals.tax} class="font-medium" />
							</span>
						</div>
					{/if}
					<div class="ca-summary-total">
						<div class="amount"><MoneyText amount={totals.total} /></div>
						<div class="label">{t('orderfe.cart.totalEstimate')}</div>
					</div>
					<p
						class="ca-muted"
						data-testid="cart-total"
						style="text-align:center;font-size:0.78rem;margin:0;"
					>
						{t('orderfe.cart.taxNote')}
					</p>
				</div>
			</div>

			{#if checkoutError}
				<Alert type="error" title={t('orderfe.cart.checkoutFailed')}>
					<span data-testid="checkout-error">{checkoutError}</span>
					{#if checkoutProfileIncomplete}
						<div style="margin-top:8px;">
							<a href="/account" data-testid="checkout-complete-profile-link" style="color:var(--ca-primary);font-weight:600;">
								{t('orderfe.cart.completeProfileLink')}
							</a>
						</div>
					{/if}
				</Alert>
			{/if}

			{#if !data.user}
				<!-- Inline login / register -->
				<section class="ca-card" data-testid="cart-auth-panel">
					<div class="ca-card-header">{t('orderfe.cart.authRequiredTitle')}</div>
					<div class="ca-card-body">
						<p class="ca-muted" style="margin:0 0 16px;">{t('orderfe.cart.authRequiredDesc')}</p>

						{#if form?.registerSuccess}
							<div class="mb-4">
								<Alert type="success" title={t('orderfe.cart.registerSuccessTitle')}>
									<span data-testid="register-success">
										{t('orderfe.cart.registerSuccessDesc', { email: form.registeredEmail ?? '' })}
									</span>
								</Alert>
							</div>
						{/if}

						<Tabs tabs={authTabs} bind:active={authTab} />

						{#if authTab === 'login'}
							<form
								method="POST"
								action="?/login"
								class="mt-4"
								use:enhance={() => {
									loggingIn = true;
									return async ({ result, update }) => {
										loggingIn = false;
										if (result.type === 'failure') captchaReset++;
										await update({ reset: false });
									};
								}}
							>
								{#if loginError}
									<div class="mb-3">
										<Alert type="error">{loginError}</Alert>
									</div>
								{/if}
								<label class="ca-label" for="cart-login-email">{t('auth.email')}</label>
								<input
									id="cart-login-email"
									type="email"
									name="email"
									class="ca-input"
									style="margin-bottom:12px;"
									bind:value={loginEmail}
									autocomplete="email"
									required
									data-testid="cart-login-email"
								/>
								<label class="ca-label" for="cart-login-password">{t('auth.password')}</label>
								<input
									id="cart-login-password"
									type="password"
									name="password"
									class="ca-input"
									style="margin-bottom:16px;"
									bind:value={loginPassword}
									autocomplete="current-password"
									required
									data-testid="cart-login-password"
								/>
								{#if data.captcha?.enabled && data.captcha.siteKey}
									<Turnstile siteKey={data.captcha.siteKey} resetSignal={captchaReset} />
								{/if}
								<button
									type="submit"
									class="ca-btn ca-btn-primary ca-btn-block"
									disabled={loggingIn}
									data-testid="cart-login-submit"
								>
									{t('auth.login')}
								</button>
								<p style="margin-top:12px;text-align:center;font-size:0.8rem;">
									<a href="/login?redirect=/order/cart" style="color:var(--ca-primary);">
										{t('orderfe.cart.fullLoginPage')}
									</a>
								</p>
							</form>
						{:else}
							<form
								method="POST"
								action="?/register"
								class="mt-4"
								data-testid="cart-register-form"
								use:enhance={() => {
									registering = true;
									return async ({ result, update }) => {
										registering = false;
										if (result.type === 'success') authTab = 'login';
										if (result.type === 'failure') captchaReset++;
										await update({ reset: false });
									};
								}}
							>
								{#if registerError}
									<div class="mb-3">
										<Alert type="error">{registerError}</Alert>
									</div>
								{/if}

								<FormField
									label={t('auth.email')}
									name="email"
									type="email"
									bind:value={regEmail}
									autocomplete="email"
									required
								/>
								<div class="grid grid-cols-1 gap-x-3 sm:grid-cols-2">
									<FormField
										label={t('auth.password')}
										name="password"
										type="password"
										bind:value={regPassword}
										autocomplete="new-password"
										required
									/>
									<FormField
										label={t('auth.confirmPassword')}
										name="confirm_password"
										type="password"
										bind:value={regConfirm}
										autocomplete="new-password"
										required
										error={form?.registerErrorKey === 'orderfe.register.passwordMismatch'
											? t('orderfe.register.passwordMismatch')
											: undefined}
									/>
									<FormField
										label={t('orderfe.register.firstName')}
										name="first_name"
										bind:value={regFirstName}
										autocomplete="given-name"
										required
									/>
									<FormField
										label={t('orderfe.register.lastName')}
										name="last_name"
										bind:value={regLastName}
										autocomplete="family-name"
										required
									/>
									<FormField
										label={t('orderfe.register.company')}
										name="company"
										bind:value={regCompany}
										autocomplete="organization"
									/>
									<FormField
										label={t('orderfe.register.phone')}
										name="phone"
										bind:value={regPhone}
										autocomplete="tel"
									/>
								</div>
								<FormField
									label={t('orderfe.register.address')}
									name="address1"
									bind:value={regAddress}
									autocomplete="street-address"
									required
								/>
								<div class="grid grid-cols-1 gap-x-3 sm:grid-cols-2">
									<FormField
										label={t('orderfe.register.city')}
										name="city"
										bind:value={regCity}
										autocomplete="address-level2"
										required
									/>
									<FormField
										label={t('orderfe.register.state')}
										name="state"
										bind:value={regState}
										autocomplete="address-level1"
										required
									/>
								</div>
								<div class="grid grid-cols-1 gap-x-3 sm:grid-cols-2">
									<FormField
										label={t('orderfe.register.postcode')}
										name="postcode"
										bind:value={regPostcode}
										autocomplete="postal-code"
										required
									/>
									<FormField
										label={t('orderfe.register.country')}
										name="country"
										bind:value={regCountry}
										autocomplete="country"
									/>
								</div>
								{#if data.captcha?.enabled && data.captcha.siteKey}
									<Turnstile siteKey={data.captcha.siteKey} resetSignal={captchaReset} />
								{/if}
								<button
									type="submit"
									class="ca-btn ca-btn-primary ca-btn-block"
									disabled={registering}
									data-testid="cart-register-submit"
								>
									{t('auth.register')}
								</button>
							</form>
						{/if}
					</div>
				</section>
			{:else if !isClient}
				<Alert type="warning">
					<span data-testid="staff-cannot-order">{t('orderfe.cart.staffCannotOrder')}</span>
				</Alert>
			{:else if !data.emailVerified}
				<!-- Verification required -->
				<section class="ca-card" data-testid="verify-required-panel">
					<div class="ca-card-body">
						<Alert type="warning" title={t('orderfe.cart.verifyRequiredTitle')}>
							{t('orderfe.cart.verifyRequiredDesc')}
						</Alert>
						<p class="ca-muted" style="margin-top:12px;font-size:0.8rem;">
							{t('orderfe.cart.loggedInAs', { email: data.user.email })}
						</p>
						{#if form?.resendSuccess}
							<div class="mt-3">
								<Alert type="success">
									<span data-testid="resend-success">{t('orderfe.cart.resendSuccess')}</span>
								</Alert>
							</div>
						{:else if form?.resendError}
							<div class="mt-3">
								<Alert type="error">{t('orderfe.cart.resendFailed')}</Alert>
							</div>
						{/if}
						<form
							method="POST"
							action="?/resend"
							class="mt-4"
							use:enhance={() => {
								resending = true;
								return async ({ update }) => {
									resending = false;
									await update({ reset: false });
								};
							}}
						>
							<button
								type="submit"
								class="ca-btn ca-btn-primary ca-btn-block"
								disabled={resending}
								data-testid="resend-verification"
							>
								{t('orderfe.cart.resend')}
							</button>
						</form>
					</div>
				</section>
			{:else}
				<section class="ca-card">
					<div class="ca-card-body">
						<p class="ca-muted" style="margin:0 0 12px;font-size:0.8rem;">
							{t('orderfe.cart.loggedInAs', { email: data.user.email })}
						</p>
						<form
							method="POST"
							action="?/checkout"
							use:enhance={() => {
								checkingOut = true;
								return async ({ result, update }) => {
									checkingOut = false;
									if (result.type === 'redirect') cart.clear();
									if (result.type === 'failure') captchaReset++;
									await update({ reset: false });
								};
							}}
						>
							<input type="hidden" name="items" value={itemsJson} />
							{#if cart.coupon}
								<input type="hidden" name="coupon" value={cart.coupon.code} />
							{/if}
							{#if data.captcha?.enabled && data.captcha.siteKey}
								<Turnstile siteKey={data.captcha.siteKey} resetSignal={captchaReset} />
							{/if}
							<button
								type="submit"
								class="ca-btn ca-btn-success ca-btn-block ca-btn-lg"
								disabled={checkingOut || cart.count === 0}
								data-testid="checkout-submit"
							>
								{checkingOut ? t('common.loading') : t('orderfe.cart.checkout')}
								<i class="fas fa-arrow-right" aria-hidden="true"></i>
							</button>
						</form>
						<p style="margin-top:12px;text-align:center;font-size:0.85rem;">
							<a href="/order" style="color:var(--ca-primary);">
								{t('orderfe.cart.continueShopping')}
							</a>
						</p>
					</div>
				</section>
			{/if}
		</div>
	</div>
{/if}
