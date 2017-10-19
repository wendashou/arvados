# Copyright (C) The Arvados Authors. All rights reserved.
#
# SPDX-License-Identifier: AGPL-3.0

class ApiClientAuthorization < ArvadosModel
  include HasUuid
  include KindAndEtag
  include CommonApiTemplate
  extend CurrentApiClient

  belongs_to :api_client
  belongs_to :user
  after_initialize :assign_random_api_token
  serialize :scopes, Array

  api_accessible :user, extend: :common do |t|
    t.add :owner_uuid
    t.add :user_id
    t.add :api_client_id
    t.add :api_token
    t.add :created_by_ip_address
    t.add :default_owner_uuid
    t.add :expires_at
    t.add :last_used_at
    t.add :last_used_by_ip_address
    t.add :scopes
  end

  UNLOGGED_CHANGES = ['last_used_at', 'last_used_by_ip_address', 'updated_at']

  def assign_random_api_token
    self.api_token ||= rand(2**256).to_s(36)
  end

  def owner_uuid
    self.user.andand.uuid
  end
  def owner_uuid_was
    self.user_id_changed? ? User.where(id: self.user_id_was).first.andand.uuid : self.user.andand.uuid
  end
  def owner_uuid_changed?
    self.user_id_changed?
  end

  def modified_by_client_uuid
    nil
  end
  def modified_by_client_uuid=(x) end

  def modified_by_user_uuid
    nil
  end
  def modified_by_user_uuid=(x) end

  def modified_at
    nil
  end
  def modified_at=(x) end

  def scopes_allow?(req_s)
    scopes.each do |scope|
      return true if (scope == 'all') or (scope == req_s) or
        ((scope.end_with? '/') and (req_s.start_with? scope))
    end
    false
  end

  def scopes_allow_request?(request)
    method = request.request_method
    if method == 'HEAD'
      (scopes_allow?(['HEAD', request.path].join(' ')) ||
       scopes_allow?(['GET', request.path].join(' ')))
    else
      scopes_allow?([method, request.path].join(' '))
    end
  end

  def logged_attributes
    super.except 'api_token'
  end

  def self.default_orders
    ["#{table_name}.id desc"]
  end

  def self.remote_host(uuid:)
    Rails.configuration.remote_hosts[uuid[0..4]] ||
      (Rails.configuration.remote_hosts_via_dns &&
       uuid[0..4]+".arvadosapi.com")
  end

  def self.validate(token:, remote:)
    return nil if !token
    remote ||= Rails.configuration.uuid_prefix

    case token[0..2]
    when 'v2/'
      _, uuid, secret = token.split('/')
      auth = ApiClientAuthorization.
             includes(:user, :api_client).
             where('uuid=? and (expires_at is null or expires_at > CURRENT_TIMESTAMP)', uuid).
             first
      if auth && auth.user &&
         (secret == auth.api_token ||
          secret == OpenSSL::HMAC.hexdigest('sha1', auth.api_token, remote))
        return auth
      elsif uuid[0..4] != Rails.configuration.uuid_prefix
        # Token was issued by a different cluster. If it's expired or
        # missing in our database, ask the originating cluster to
        # [re]validate it.
        arv = Arvados.new(api_host: remote_host(uuid: uuid),
                          api_token: token)
        remote_user = arv.user.current(remote_id: Rails.configuration.uuid_prefix)
        if remote_user && remote_user[:uuid][0..4] == uuid[0..4]
          act_as_system_user do
            # Add/update user and token in our database so we can
            # validate subsequent requests faster.
            user = User.find_or_create_by(uuid: remote_user[:uuid])
            user.update_attributes!(remote_user)
            auth = ApiClientAuthorization.
                   includes(:user).
                   find_or_create_by(uuid: uuid,
                                     api_token: token,
                                     user: user,
                                     api_client_id: 0)
            # Accept this token (and don't reload the user record) for
            # 5 minutes. TODO: Request the actual api_client_auth
            # record from the remote server in case it wants the token
            # to expire sooner.
            auth.update_attributes!(expires_at: Time.now + 5.minutes)
          end
          return auth
        end
      end
    else
      auth = ApiClientAuthorization.
             includes(:user, :api_client).
             where('api_token=? and (expires_at is null or expires_at > CURRENT_TIMESTAMP)', token).
             first
      if auth && auth.user
        return auth
      end
    end
    return nil
  end

  protected

  def permission_to_create
    current_user.andand.is_admin or (current_user.andand.id == self.user_id)
  end

  def permission_to_update
    (permission_to_create and
     not uuid_changed? and
     not user_id_changed? and
     not owner_uuid_changed?)
  end

  def log_update
    super unless (changed - UNLOGGED_CHANGES).empty?
  end
end
